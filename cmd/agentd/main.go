// Command agentd 是 AgentHub 的服务入口。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/moying/agenthub/internal/admin"
	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/agent/pi"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/httpapi"
	"github.com/moying/agenthub/internal/modelcfg"
	"github.com/moying/agenthub/internal/modelrouter"
	"github.com/moying/agenthub/internal/obs"
	"github.com/moying/agenthub/internal/platform/dingtalk"
	"github.com/moying/agenthub/internal/platform/feishu"
	"github.com/moying/agenthub/internal/scheduler"
	"github.com/moying/agenthub/internal/store"
	"github.com/moying/agenthub/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(healthcheck())
	}
	os.Exit(run())
}

// run 执行启动流程并返回退出码。
//
// @remarks 配置错误返回 2（便于编排系统区分「配置问题」与「运行故障」），
// 其他启动失败返回 1，正常退出返回 0。
// healthcheck 供容器 HEALTHCHECK 使用：探测本机 /healthz。
func healthcheck() int {
	addr := os.Getenv("AGENTHUB_HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8787"
	}
	addr = strings.Replace(addr, "0.0.0.0", "127.0.0.1", 1)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"ok"`) {
		return 1
	}
	return 0
}

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
	var dt *dingtalk.Channel
	feishuStatus := func() string { return "off" }
	if cfg.Channel == config.ChannelFeishu {
		client := feishu.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
		ch = feishu.NewChannel(client)
	}
	if cfg.Channel == config.ChannelDingtalk {
		dt = dingtalk.New(dingtalk.Config{
			ClientID:       cfg.Dingtalk.ClientID,
			ClientSecret:   cfg.Dingtalk.ClientSecret,
			RequireMention: cfg.Dingtalk.RequireMention,
			AllowedUsers:   cfg.Dingtalk.AllowedUsers,
			AllowedChats:   cfg.Dingtalk.AllowedChats,
			BotName:        cfg.Dingtalk.BotName,
			ModelName:      cfg.Agent.Model,
			ModelBaseURL:   cfg.Agent.BaseURL,
		}, nil, logger)
		ch = dt
	}

	profiles := store.NewProfileStore(cfg.Storage.Dir)

	modelStore := modelcfg.NewStore(filepath.Join(cfg.Storage.Dir, "settings.yaml"))
	modelConfig, err := modelStore.Load()
	if err != nil {
		logger.Error("load model config failed", "error", err)
		return 1
	}
	if len(modelConfig.Providers) == 0 {
		legacyStore := store.NewModelSettingsStore(cfg.Storage.Dir)
		if saved, exists, err := legacyStore.Load(); err == nil && exists {
			if saved.BaseURL != "" {
				cfg.Agent.BaseURL = saved.BaseURL
			}
			if saved.Model != "" {
				cfg.Agent.Model = saved.Model
			}
			if saved.APIKey != "" {
				cfg.Agent.APIKey = saved.APIKey
			}
			cfg.Agent.FallbackModel = saved.FallbackModel
			cfg.Agent.VisionModel = saved.VisionModel
		}
		modelConfig = modelcfg.Legacy(cfg.Agent.BaseURL, cfg.Agent.APIKey, cfg.Agent.Model, cfg.Agent.FallbackModel, cfg.Agent.VisionModel)
		if err := modelStore.Save(modelConfig); err != nil {
			logger.Warn("persist migrated model config failed", "error", err)
		}
	}

	metrics := obs.NewMetrics()
	routerToken := randomToken()
	router := modelrouter.New(modelConfig, routerToken, metrics, logger)
	if err := router.Start(); err != nil {
		logger.Error("model router start failed", "error", err)
		return 1
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = router.Close(ctx)
	}()

	piRoot := filepath.Join(cfg.Storage.Dir, "pi")
	piAgentDir := cfg.Pi.AgentDir
	if piAgentDir == "" {
		piAgentDir = filepath.Join(piRoot, "agent")
	}
	piSessionDir := cfg.Pi.SessionDir
	if piSessionDir == "" {
		piSessionDir = filepath.Join(piRoot, "sessions")
	}
	piWorkspaceDir := cfg.Pi.WorkspaceDir
	if piWorkspaceDir == "" {
		piWorkspaceDir = filepath.Join(piRoot, "workspaces")
	}
	preparePi := func(current modelcfg.Config) error {
		route, ok := current.Route("main")
		if !ok {
			return fmt.Errorf("模型配置缺少 main 线路")
		}
		window, maxTokens := 0, 0
		images := false
		for _, id := range route.Candidates {
			model, ok := current.Model(id)
			if !ok {
				continue
			}
			if window == 0 || (model.ContextWindow > 0 && model.ContextWindow < window) {
				window = model.ContextWindow
			}
			if maxTokens == 0 || (model.MaxTokens > 0 && model.MaxTokens < maxTokens) {
				maxTokens = model.MaxTokens
			}
			if model.Supports("vision") {
				images = true
			}
		}
		return pi.PrepareAgentDir(pi.AgentConfig{
			Dir:            piAgentDir,
			RouterURL:      router.URL(),
			RouterToken:    router.Token(),
			RouteName:      route.Name,
			ContextWindow:  window,
			MaxTokens:      maxTokens,
			SupportsImages: images,
			SystemPrompt:   pi.SystemPrompt(),
			SkillsSrc:      skillsDir,
			MCPFile:        filepath.Join(cfg.Storage.Dir, "mcp.yaml"),
		})
	}
	if err := preparePi(modelConfig); err != nil {
		logger.Error("pi agent dir 初始化失败", "error", err)
		return 1
	}
	if err := pi.SyncAgentConfig(piWorkspaceDir, piAgentDir); err != nil {
		logger.Warn("sync pi workspaces failed", "error", err)
	}
	piVersion := detectPiVersion(cfg.Pi.Bin)
	logger.Info("pi runtime", "bin", cfg.Pi.Bin, "version", piVersion)
	runner := pi.New(pi.Config{
		Bin:          cfg.Pi.Bin,
		Tools:        cfg.Pi.Tools,
		AgentDir:     piAgentDir,
		SessionDir:   piSessionDir,
		WorkspaceDir: piWorkspaceDir,
		Isolation:    cfg.Pi.Isolation,
		Timeout:      time.Duration(cfg.Pi.TimeoutSeconds) * time.Second,
		Logger:       logger,
		Provider:     "agenthub",
		Model:        "main",
	})
	engine := core.New(runner, ch, logger)
	engine.SetGenerationCleaner(func(conv string, generation int) {
		if err := runner.Cleanup(conv, generation); err != nil {
			logger.Warn("cleanup generation failed", "conversation", conv, "generation", generation, "error", err)
		}
	})
	engine.SetConcurrency(cfg.MaxConcurrentRuns)
	engine.SetRateLimit(cfg.RateLimitPerMin, time.Minute)
	engine.SetProfileSource(func() core.ProfileSelection {
		active := profiles.Active()
		return core.ProfileSelection{Name: active.Name, Skills: active.Skills, MCPs: active.MCPs}
	})
	if fc, ok := ch.(*feishu.Channel); ok {
		engine.SetPresenter(feishu.NewCardPresenter(fc.Client(), logger).WithStopButton(cfg.Feishu.CardActionEnabled))
	}
	if dt != nil {
		dt.SetEngine(engine)
	}

	var journal *store.Journal
	if db != nil {
		journal = store.NewJournal(db)
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
	var taskScheduler *scheduler.Scheduler
	if journal != nil {
		taskScheduler = scheduler.New(journal, engine, logger)
		go taskScheduler.Run(workCtx)
	}

	engine.SetMetrics(metrics)
	srv := httpapi.New(cfg, logger, metrics, db, version.Version)
	if db != nil {
		srv.RegisterCheck("db", func() (bool, string) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := db.Pool().Ping(ctx); err != nil {
				return false, err.Error()
			}
			return true, "ok"
		})
	}

	if cfg.Channel == config.ChannelFeishu {
		feishuStatus = startFeishu(ctx, cfg, logger, stop, srv, engine, ch, attachmentsDir, journal)
	}
	if dt != nil {
		go func() {
			if err := dt.Start(workCtx); err != nil {
				logger.Error("dingtalk stream stopped", "error", err)
				stop()
			}
		}()
		feishuStatus = func() string { return "connected" }
		srv.RegisterCheck("dingtalk", func() (bool, string) { return true, "stream" })
	}

	adminStore, err := admin.NewStoreWithSkills(cfg.Storage.Dir, filepath.Join(piAgentDir, "skills"))
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
	adminSrv := admin.New(cfg, engine, adminStore, admin.Options{
		Version:      version.Version,
		FeishuStatus: feishuStatus,
		Agent:        runner,
		Tools:        nil,
		Runtime:      "pi",
		PiVersion:    piVersion,
		Profiles:     profiles,
		History: func(ctx context.Context) ([]store.Generation, error) {
			if journal == nil {
				return nil, nil
			}
			return journal.ListGenerations(ctx, 100)
		},
		HistoryMessages: func(ctx context.Context, conv string, generation int) ([]core.Message, error) {
			if journal == nil {
				return nil, nil
			}
			return journal.ListGenerationMessages(ctx, conv, generation, 200)
		},
		AuditRecord: func(actor, action, target, detail string) {
			if journal == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := journal.SaveAudit(ctx, actor, action, target, detail); err != nil {
				logger.Debug("audit write failed", "error", err)
			}
		},
		AuditList: func(ctx context.Context, limit int) ([]store.AuditEntry, error) {
			if journal == nil {
				return nil, nil
			}
			return journal.ListAudit(ctx, limit)
		},
		Journal:   journal,
		Scheduler: taskScheduler,
		ReloadAgentConfig: func() error {
			if err := preparePi(router.Config()); err != nil {
				return err
			}
			return pi.SyncAgentConfig(piWorkspaceDir, piAgentDir)
		},
		ModelConfig: func() modelcfg.Config { return router.Config() },
		SetModelConfig: func(v modelcfg.Config) error {
			if err := modelStore.Save(v); err != nil {
				return err
			}
			router.Update(v)
			if err := preparePi(v); err != nil {
				return err
			}
			return pi.SyncAgentConfig(piWorkspaceDir, piAgentDir)
		},
		ModelHealth: router.Health,
		TestModel: func(ctx context.Context, route string) error {
			payload := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"max_tokens":8}`, route)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, router.URL()+"/chat/completions", strings.NewReader(payload))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+router.Token())
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
				return fmt.Errorf("线路测试失败（%d）：%s", resp.StatusCode, string(raw))
			}
			return nil
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
	go workspaceCleanupLoop(workCtx, piWorkspaceDir, piSessionDir, cfg.RetentionDays, logger)

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

func startFeishu(ctx context.Context, cfg *config.Config, logger *slog.Logger, stop context.CancelFunc, health *httpapi.Server, engine *core.Engine, ch channel.Channel, attachmentsDir string, journal *store.Journal) func() string {
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
		OnAccepted: func(ctx context.Context, in channel.Inbound) {
			if client == nil {
				return
			}
			conv := in.Tenant + ":" + in.Chat.ID + ":" + in.Sender.OpenID
			chatName, _, err := client.ChatInfo(ctx, in.Chat.ID)
			if err != nil {
				logger.Debug("feishu chat info failed", "chat", in.Chat.ID, "error", err)
			}
			userName, err := client.UserName(ctx, in.Sender.OpenID)
			if err != nil {
				logger.Debug("feishu user name failed", "user", in.Sender.OpenID, "error", err)
			}
			if chatName == "" && userName == "" {
				return
			}
			engine.SetIdentity(conv, chatName, userName)
			if journal != nil {
				saveCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := journal.SaveIdentity(saveCtx, conv, chatName, userName); err != nil {
					logger.Debug("save identity failed", "error", err)
				}
			}
		},
	}, logger)

	if client != nil {
		// 启动时补齐历史会话展示名（飞书群名/用户名）。
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			for _, info := range engine.SessionInfos() {
				if info.ChatName != "" || info.UserName != "" {
					continue
				}
				parts := strings.SplitN(info.ID, ":", 3)
				if len(parts) != 3 {
					continue
				}
				chatName, _, err := client.ChatInfo(ctx, parts[1])
				if err != nil {
					logger.Debug("feishu chat info failed", "chat", parts[1], "error", err)
				}
				userName, err := client.UserName(ctx, parts[2])
				if err != nil {
					logger.Debug("feishu user name failed", "user", parts[2], "error", err)
				}
				if chatName == "" && userName == "" {
					continue
				}
				engine.SetIdentity(info.ID, chatName, userName)
				if journal != nil {
					if err := journal.SaveIdentity(ctx, info.ID, chatName, userName); err != nil {
						logger.Debug("save identity failed", "error", err)
					}
				}
			}
		}()
	}

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
// randomToken 生成内部路由的随机 token。
func randomToken() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

// detectPiVersion 读取 pi 版本，失败返回空。
func detectPiVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// workspaceCleanupLoop 定期清理超过保留期的 pi 工作区与会话文件。
func workspaceCleanupLoop(ctx context.Context, workspaces, sessions string, days int, logger *slog.Logger) {
	if days <= 0 {
		return
	}
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().AddDate(0, 0, -days)
			removed, err := pi.CleanupExpired(workspaces, sessions, cutoff)
			if err != nil {
				logger.Warn("pi workspace cleanup failed", "error", err)
			} else if removed > 0 {
				logger.Info("pi workspace cleanup", "removed", removed)
			}
		}
	}
}

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
			engine.Dispatch(ctx)
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
