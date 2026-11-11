// Command agentd 是 AgentHub 的服务入口。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/moying/agenthub/internal/admin"
	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/httpapi"
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
	logger.Info("starting", "version", version.Version, "channel", cfg.Channel, "env", cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	var ch channel.Channel
	feishuStatus := func() string { return "off" }
	if cfg.Channel == config.ChannelFeishu {
		client := feishu.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
		ch = feishu.NewChannel(client)
	}

	engine := core.New(newAgent(cfg, skillsDir, logger), ch, logger)
	metrics := obs.NewMetrics()
	srv := httpapi.New(cfg, logger, metrics, db, version.Version)

	if cfg.Channel == config.ChannelFeishu {
		feishuStatus = startFeishu(ctx, cfg, logger, stop, srv, engine)
	}

	adminStore, err := admin.NewStore(cfg.Storage.Dir)
	if err != nil {
		logger.Error("admin storage unavailable", "error", err)
		return 1
	}
	adminSrv := admin.New(cfg, engine, adminStore, admin.Options{
		Version:      version.Version,
		FeishuStatus: feishuStatus,
	}, logger)

	go func() {
		if err := srv.Run(ctx); err != nil {
			logger.Error("http server stopped", "error", err)
			stop()
		}
	}()
	go func() {
		if err := adminSrv.Run(ctx); err != nil {
			logger.Error("admin server stopped", "error", err)
			stop()
		}
	}()
	go flushLoop(ctx, engine, logger)

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := waitShutdown(shutdownCtx, db); err != nil {
		logger.Error("shutdown incomplete", "error", err)
		return 1
	}
	logger.Info("stopped")
	return 0
}

func newAgent(cfg *config.Config, skillsDir string, logger *slog.Logger) agent.Agent {
	if cfg.Agent.APIKey == "" {
		logger.Warn("model api key not configured; falling back to echo agent")
		return agent.Echo{}
	}
	logger.Info("model configured", "provider", cfg.Agent.Provider, "model", cfg.Agent.Model, "base_url", cfg.Agent.BaseURL)
	return &agent.Chat{
		BaseURL:      cfg.Agent.BaseURL,
		APIKey:       cfg.Agent.APIKey,
		Model:        cfg.Agent.Model,
		SystemPrompt: cfg.Agent.SystemPrompt,
		SkillsDir:    skillsDir,
	}
}

// startFeishu 装配事件来源并返回通道状态查询函数。
//
// @remarks 长连接与 webhook 共用同一个 Dispatcher，保证受理策略一致。
func startFeishu(ctx context.Context, cfg *config.Config, logger *slog.Logger, stop context.CancelFunc, health *httpapi.Server, engine *core.Engine) func() string {
	dispatcher := feishu.NewDispatcher(engine, feishu.Options{
		BotOpenID:      cfg.Feishu.BotOpenID,
		RequireMention: cfg.Feishu.RequireMention,
		AllowedUsers:   cfg.Feishu.AllowedUsers,
		AllowedChats:   cfg.Feishu.AllowedChats,
	}, logger)

	if cfg.Feishu.Transport == "webhook" {
		hook := feishu.NewWebhook(dispatcher, cfg.Feishu.VerificationToken, logger)
		logger.Info("feishu transport", "mode", "webhook", "addr", cfg.Webhook.Addr)
		go func() {
			if err := httpapi.Serve(ctx, cfg.Webhook.Addr, hook, logger); err != nil {
				logger.Error("webhook server stopped", "error", err)
				stop()
			}
		}()
		health.RegisterCheck("feishu", func() (bool, string) { return true, "webhook" })
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
