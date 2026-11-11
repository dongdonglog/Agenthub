// Package httpapi 提供健康检查、指标与管理面的 HTTP 入口。
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/obs"
	"github.com/moying/agenthub/internal/store"
)

// Server 承载对外 HTTP 端点。
//
// @remarks 只暴露健康检查与指标；业务入口由通道层直接消费，不经过 HTTP。
type Server struct {
	cfg     *config.Config
	log     *slog.Logger
	metrics *obs.Metrics
	db      *store.DB
	version string

	mu     sync.RWMutex
	checks map[string]func() (bool, string)
}

// New 构造 HTTP 服务。
func New(cfg *config.Config, log *slog.Logger, metrics *obs.Metrics, db *store.DB, version string) *Server {
	return &Server{
		cfg:     cfg,
		log:     log,
		metrics: metrics,
		db:      db,
		version: version,
		checks:  make(map[string]func() (bool, string)),
	}
}

// RegisterCheck 注册一个运行期健康检查。
//
// @remarks 检查函数会被并发调用，必须无副作用；返回 false 时 /healthz 返回 503。
func (s *Server) RegisterCheck(name string, fn func() (bool, string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks[name] = fn
}

// Handler 返回路由表。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.Handle("GET /metrics", s.metrics.Handler())
	mux.HandleFunc("GET /", s.handleIndex)
	return s.withAccessLog(mux)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"service": "agenthub",
		"version": s.version,
		"channel": string(s.cfg.Channel),
	})
}

// Run 启动监听并在 ctx 结束时优雅关闭。
//
// @throws 监听失败时返回错误；正常关闭返回 nil。
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.HTTP.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http listening", "addr", s.cfg.HTTP.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
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

func (s *Server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.metrics.Inc("agenthub_http_requests_total", map[string]string{
			"method": r.Method,
			"path":   r.URL.Path,
		})
		s.log.Debug("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})
}
