// Command agentd 是 AgentHub 的服务入口。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moying/agenthub/internal/agent"
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

	metrics := obs.NewMetrics()
	srv := httpapi.New(cfg, logger, metrics, db, version.Version)

	if cfg.Channel == config.ChannelFeishu {
		startFeishu(ctx, cfg, logger, stop)
	}

	go func() {
		if err := srv.Run(ctx); err != nil {
			logger.Error("http server stopped", "error", err)
			stop()
		}
	}()

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

// startFeishu 装配飞书通道、事件入口与处理循环。
//
// @remarks 事件入口单独监听；处理循环用短周期 Flush 把排队消息收敛成批次。
func startFeishu(ctx context.Context, cfg *config.Config, logger *slog.Logger, stop context.CancelFunc) {
	client := feishu.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
	engine := core.New(agent.Echo{}, feishu.NewChannel(client), logger)

	hook := feishu.NewWebhook(engine, feishu.WebhookOptions{
		VerificationToken: cfg.Feishu.VerificationToken,
		BotOpenID:         cfg.Feishu.BotOpenID,
		RequireMention:    cfg.Feishu.RequireMention,
		AllowedUsers:      cfg.Feishu.AllowedUsers,
		AllowedChats:      cfg.Feishu.AllowedChats,
	}, logger)

	go func() {
		if err := httpapi.Serve(ctx, cfg.Webhook.Addr, hook, logger); err != nil {
			logger.Error("webhook server stopped", "error", err)
			stop()
		}
	}()

	go func() {
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
	}()
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
