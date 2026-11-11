package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// Serve 在独立监听上提供 handler，并在 ctx 结束时优雅关闭。
//
// @remarks 用于飞书事件回调等需要单独暴露的入口；与健康检查监听分离，
// 便于分别做网络策略。
func Serve(ctx context.Context, addr string, handler http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("webhook listening", "addr", addr)
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
