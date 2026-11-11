// Command agentd 是 AgentHub 的服务入口。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/moying/agenthub/internal/admin"
	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/httpapi"
	"github.com/moying/agenthub/internal/mcp"
	"github.com/moying/agenthub/internal/obs"
	"github.com/moying/agenthub/internal/platform/feishu"
	"github.com/moying/agenthub/internal/store"
	"github.com/moying/agenthub/internal/version"
)

func main() {
	os.Exit(run())
}

// run 执行启动流程并返回退出码。
//
// @remarks 配置错误返回 2（便于编排系统区分「配置问题」与「运行故障」），
// 其他启动失败返回 1，正常退出返回 0。
func run() int {
	envFile := os.Getenv("AGENTHUB_ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	if err := config.LoadDotEnv(envFile); err != nil {
		fmt.Fprintf(os.Stderr, "环境文件错误：%v\n", err)
		return 2
	}

	cfg, err := config.Load(os.Getenv("AGENTHUB_CONFIG"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置错误：%v\n", err)
		return 2
	}

	logger := obs.NewLogger(cfg.Log.Level, cfg.Log.Format, os.Stdout)
	if cfg.Admin.Token == "" && cfg.Env == "production" {
		logger.Warn("AGENTHUB_ADMIN_TOKEN 未设置：管理台无鉴权，请确保只在内网或网关后暴露")
	}
	logger.Info("starting", "version", version.Version, "channel", cfg.Channel, "env", cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// 工作上下文与信号解耦：收到信号后先等在途运行收敛，再取消。
	workCtx, workCancel := context.WithCancel(context.Background())
	defer workCancel()
	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	db, err := store.Open(ctx, cfg.DB.URL, cfg.DB.MaxConns)
	if err != nil {
		logger.Error("database unavailable", "error", err)
		return 1
	}
	if db == nil {
		logger.Warn("database not configured; running without persistence")
	}
	defer db.Close()

	skillsDir := cfg.Agent.SkillsDir
	if skillsDir == "" {
		skillsDir = filepath.Join(cfg.Storage.Dir, "skills")
	}
	attachmentsDir := filepath.Join(cfg.Storage.Dir, "attachments")
	seedDir := os.Getenv("AGENTHUB_SEED_SKILLS_DIR")
	if seedDir == "" {
		seedDir = "skills"
	}
	if copied, err := agent.SeedSkills(seedDir, skillsDir); err != nil {
		logger.Warn("seed skills failed", "error", err)
	} else if copied > 0 {
		logger.Info("seeded skills", "count", copied, "dir", skillsDir)
	}

	var ch channel.Channel
	feishuStatus := func() string { return "off" }
	if cfg.Channel == config.ChannelFeishu {
		client := feishu.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
		ch = feishu.NewChannel(client)
	}

	mcpManager := mcp.StartAll(ctx, filepath.Join(cfg.Storage.Dir, "mcp.yaml"), logger)
	defer mcpManager.Close()

	tools := make([]agent.Tool, 0, len(mcpManager.Tools())+1)
	tools = append(tools, mcpManager.Tools()...)
	tools = append(tools, &agent.ReadFileTool{Roots: []string{attachmentsDir, skillsDir}})
	if summary := mcpManager.Summary(); summary != "" {
		logger.Info("mcp loaded", "summary", summary)
	}

	profiles := store.NewProfileStore(cfg.Storage.Dir)
	runner := newAgent(cfg, skillsDir, tools, profiles, logger)
	engine := core.New(runner, ch, logger)
	engine.SetProfileSource(func() core.ProfileSelection {
		active := profiles.Active()
		return core.ProfileSelection{Name: active.Name, Skills: active.Skills, MCPs: active.MCPs}
	})
	if fc, ok := ch.(*feishu.Channel); ok {
		engine.SetPresenter(feishu.NewCardPresenter(fc.Client(), logger).WithStopButton(cfg.Feishu.CardActionEnabled))
	}

	if db != nil {
		if err := store.Migrate(ctx, db, migrationsDir()); err != nil {
			logger.Error("migration failed", "error", err)
			return 1
		}
		engine.SetJournal(store.NewJournal(db))
		if err := engine.Restore(ctx); err != nil {
			logger.Error("restore failed", "error", err)
			return 1
		}
		logger.Info("state restored")
	}
	metrics := obs.NewMetrics()
	engine.SetMetrics(metrics)
	srv := httpapi.New(cfg, logger, metrics, db, version.Version)

	if cfg.Channel == config.ChannelFeishu {
		feishuStatus = startFeishu(ctx, cfg, logger, stop, srv, engine, ch, attachmentsDir)
	}

	adminStore, err := admin.NewStore(cfg.Storage.Dir)
	if err != nil {
		logger.Error("admin storage unavailable", "error", err)
		return 1
	}
	if list, err := profiles.ListProfiles(); err == nil && len(list) == 0 {
		// 首次启动：把当前可关联技能收进默认档案，便于管理台展示引用关系。
		var names []string
		if skills, err := adminStore.ListSkills(); err == nil {
			for _, sk := range skills {
				if sk.Status == "active" {
					names = append(names, sk.Name)
				}
			}
		}
		if err := profiles.PutProfile(store.Profile{Name: "default", Description: "默认档案", Skills: names}); err != nil {
			logger.Warn("seed default profile failed", "error", err)
		}
	}
	journal := store.NewJournal(db)
	adminSrv := admin.New(cfg, engine, adminStore, admin.Options{
		Version:      version.Version,
		FeishuStatus: feishuStatus,
		Agent:        runner,
		Tools:        tools,
		Profiles:     profiles,
		History: func(ctx context.Context) ([]store.Generation, error) {
			if journal == nil {
				return nil, nil
			}
			return journal.ListGenerations(ctx, 100)
		},
		AuditRecord: func(action, target, detail string) {
			if journal == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := journal.SaveAudit(ctx, action, target, detail); err != nil {
				logger.Debug("audit write failed", "error", err)
			}
		},
		AuditList: func(ctx context.Context, limit int) ([]store.AuditEntry, error) {
			if journal == nil {
				return nil, nil
			}
			return journal.ListAudit(ctx, limit)
		},
	}, logger)

	go func() {
		if err := srv.Run(serverCtx); err != nil {
			logger.Error("http server stopped", "error", err)
			stop()
		}
	}()
	go func() {
		if err := adminSrv.Run(serverCtx); err != nil {
			logger.Error("admin server stopped", "error", err)
			stop()
		}
	}()
	go flushLoop(workCtx, engine, logger)
	if db != nil {
		go purgeLoop(ctx, db, cfg.RetentionDays, logger)
	}

	<-ctx.Done()
	logger.Info("shutting down; draining in-flight runs")

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 20*time.Second)
	if !engine.WaitIdle(drainCtx) {
		logger.Warn("drain timeout; interrupting in-flight runs")
	}
	drainCancel()
	workCancel()
	serverCancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := waitShutdown(shutdownCtx, db); err != nil {
		logger.Error("shutdown incomplete", "error", err)
		return 1
	}
	logger.Info("stopped")
	return 0
}

func migrationsDir() string {
	if dir := os.Getenv("AGENTHUB_MIGRATIONS_DIR"); dir != "" {
		return dir
	}
	return "migrations"
}

func newAgent(cfg *config.Config, skillsDir string, tools []agent.Tool, profiles *store.ProfileStore, logger *slog.Logger) agent.Agent {
	if cfg.Agent.APIKey == "" {
		logger.Warn("model api key not configured; falling back to echo agent")
		return agent.Echo{}
	}
	logger.Info("model configured", "provider", cfg.Agent.Provider, "model", cfg.Agent.Model, "base_url", cfg.Agent.BaseURL)
	skillFilter := func(name string) bool {
		active := profiles.Active()
		if len(active.Skills) == 0 {
			return true
		}
		for _, item := range active.Skills {
			if item == name {
				return true
			}
		}
		return false
	}
	toolFilter := func(name string) bool {
		if !strings.HasPrefix(name, "mcp_") {
			return true
		}
		active := profiles.Active()
		if len(active.MCPs) == 0 {
			return true
		}
		for _, server := range active.MCPs {
			if mcp.ToolMatchesServer(name, server) {
				return true
			}
		}
		return false
	}

	return &agent.Chat{
		BaseURL:       cfg.Agent.BaseURL,
		APIKey:        cfg.Agent.APIKey,
		Model:         cfg.Agent.Model,
		FallbackModel: cfg.Agent.FallbackModel,
		VisionModel:   cfg.Agent.VisionModel,
		SystemPrompt:  cfg.Agent.SystemPrompt,
		SkillsDir:     skillsDir,
		Tools:         tools,
		Logger:        logger,
		SkillFilter:   skillFilter,
		ToolFilter:    toolFilter,
	}
}

// startFeishu 装配事件来源并返回通道状态查询函数。
//
// @remarks 长连接与 webhook 共用同一个 Dispatcher，保证受理策略一致。
func startFeishu(ctx context.Context, cfg *config.Config, logger *slog.Logger, stop context.CancelFunc, health *httpapi.Server, engine *core.Engine, ch channel.Channel, attachmentsDir string) func() string {
	var client *feishu.Client
	if fc, ok := ch.(*feishu.Channel); ok {
		client = fc.Client()
	}
	dispatcher := feishu.NewDispatcher(engine, feishu.Options{
		BotOpenID:        cfg.Feishu.BotOpenID,
		RequireMention:   cfg.Feishu.RequireMention,
		AllowedUsers:     cfg.Feishu.AllowedUsers,
		AllowedChats:     cfg.Feishu.AllowedChats,
		Client:           client,
		AttachmentsDir:   attachmentsDir,
		MentionFreeChats: cfg.Feishu.MentionFreeChats,
		ModelName:        cfg.Agent.Model,
		ModelBaseURL:     cfg.Agent.BaseURL,
	}, logger)

	mux := http.NewServeMux()
	serving := false
	serve := func() {
		if serving {
			return
		}
		serving = true
		go func() {
			if err := httpapi.Serve(ctx, cfg.Webhook.Addr, mux, logger); err != nil {
				logger.Error("webhook server stopped", "error", err)
				stop()
			}
		}()
	}

	if cfg.Feishu.Transport == "webhook" {
		hook := feishu.NewWebhook(dispatcher, cfg.Feishu.VerificationToken, cfg.Feishu.EncryptKey, logger)
		mux.Handle("/feishu/events", hook)
		logger.Info("feishu transport", "mode", "webhook", "addr", cfg.Webhook.Addr)
		health.RegisterCheck("feishu", func() (bool, string) { return true, "webhook" })
	}
	if cfg.Feishu.CardActionEnabled {
		mux.Handle("/feishu/card", feishu.NewCardAction(engine, cfg.Feishu.VerificationToken, cfg.Feishu.EncryptKey, logger))
		logger.Info("card action endpoint enabled", "path", "/feishu/card")
	}
	if cfg.Feishu.Transport == "webhook" || cfg.Feishu.CardActionEnabled {
		serve()
	}
	if cfg.Feishu.Transport == "webhook" {
		return func() string { return "webhook" }
	}

	connector := feishu.NewWSConnector(cfg.Feishu.AppID, cfg.Feishu.AppSecret, cfg.Feishu.Domain, logger)
	health.RegisterCheck("feishu", func() (bool, string) {
		if connector.Connected() {
			return true, "connected"
		}
		if connector.Ready() {
			return true, "reconnecting"
		}
		return true, "connecting"
	})
	logger.Info("feishu transport", "mode", "websocket", "domain", cfg.Feishu.Domain)
	go func() {
		// 长连接断开后按指数退避重试，不让一次网络抖动带走整个服务。
		backoff := time.Second
		for ctx.Err() == nil {
			err := connector.Run(ctx, dispatcher.HandlePayload)
			if ctx.Err() != nil {
				return
			}
			logger.Error("websocket connector stopped; retrying", "error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}()
	return func() string {
		switch {
		case connector.Connected():
			return "connected"
		case connector.Ready():
			return "reconnecting"
		default:
			return "connecting"
		}
	}
}

// purgeLoop 定期清理过期历史数据。
func purgeLoop(ctx context.Context, db *store.DB, days int, logger *slog.Logger) {
	if days <= 0 {
		return
	}
	journal := store.NewJournal(db)
	run := func() {
		cutoff := time.Now().AddDate(0, 0, -days)
		deleted, err := journal.PurgeBefore(ctx, cutoff)
		if err != nil {
			logger.Error("retention purge failed", "error", err)
			return
		}
		if deleted > 0 {
			logger.Info("retention purge", "rows", deleted, "cutoff", cutoff.Format(time.RFC3339))
		}
	}
	run()
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func flushLoop(ctx context.Context, engine *core.Engine, logger *slog.Logger) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			go func() {
				if err := engine.Flush(ctx); err != nil {
					logger.Error("flush failed", "error", err)
				}
			}()
		}
	}
}

func waitShutdown(ctx context.Context, db *store.DB) error {
	done := make(chan struct{})
	go func() {
		db.Close()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
