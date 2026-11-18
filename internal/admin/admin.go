package admin

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/modelcfg"
	"github.com/moying/agenthub/internal/modelrouter"
	"github.com/moying/agenthub/internal/scheduler"
	"github.com/moying/agenthub/internal/store"
)

//go:embed static
var staticFS embed.FS

var staticSub = func() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}()

// Options 是管理面的运行期信息。

type Options struct {
	// Version 是构建版本。
	Version string
	// FeishuStatus 返回通道连接状态：connected / reconnecting / connecting。
	FeishuStatus func() string
	// Agent 用于管理台的在线试跑。
	Agent agent.Agent
	// Runtime 是 agent 运行时名称（当前固定 pi）。
	Runtime string
	// PiVersion 是检测到的 pi 版本。
	PiVersion string
	// Profiles 是资源配置档案存储。
	Profiles *store.ProfileStore
	// History 返回历史会话代。
	History func(ctx context.Context) ([]store.Generation, error)
	// HistoryMessages 读取某个会话代的消息。
	HistoryMessages func(ctx context.Context, conv string, generation int) ([]core.Message, error)
	// AuditRecord 追加一条审计；为空表示不记录。
	AuditRecord func(actor, action, target, detail string)
	// AuditList 读取最近审计。
	AuditList func(ctx context.Context, limit int) ([]store.AuditEntry, error)
	// ModelConfig 返回当前模型路由配置（含明文 Key，仅内部使用）。
	ModelConfig func() modelcfg.Config
	// SetModelConfig 保存模型路由配置并热更新。
	SetModelConfig func(modelcfg.Config) error
	// ModelHealth 返回候选健康状态。
	ModelHealth func() []modelrouter.CandidateHealth
	// TestModel 测试某条线路。
	TestModel func(ctx context.Context, route string) error
	// Journal 用于定时任务读写；为空表示不启用定时任务管理。
	Journal *store.Journal
	// Scheduler 用于立即执行定时任务。
	Scheduler *scheduler.Scheduler
	// ReloadAgentConfig 在 MCP/模型配置变更后重新生成 pi 配置。
	ReloadAgentConfig func() error
	// Incidents 是 AIOps 事故存储；为空表示未启用。
	Incidents *store.IncidentStore
	// Cases 是统一案件与数字员工存储；为空表示未启用。
	Cases *store.CaseStore
	// ExecuteAction 执行已批准动作；为空表示只记录审批。
	ExecuteAction func(ctx context.Context, approval *store.Approval) (string, error)
	// NotifyApproval 把审批卡片发到案件群；为空表示不发送。
	NotifyApproval func(ctx context.Context, approval *store.Approval) error
	// CreateTicket 手动创建支持工单。
	CreateTicket func(ctx context.Context, title, summary, service, environment, priority, requester string) (string, error)
	// SendReport 把处置报告发到案件群。
	SendReport func(ctx context.Context, caseID, text string) error
}

// Server 提供管理页面与管理 API。
//
// @remarks 管理面没有内置登录，必须由网关或内网边界保护。

type Server struct {
	cfg    *config.Config
	engine *core.Engine
	store  *Store
	opts   Options
	log    *slog.Logger
}

// New 构造管理面。

func New(cfg *config.Config, engine *core.Engine, store *Store, opts Options, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	if opts.FeishuStatus == nil {
		opts.FeishuStatus = func() string { return "unknown" }
	}
	return &Server{cfg: cfg, engine: engine, store: store, opts: opts, log: log}
}

// Handler 返回路由表。

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /admin/", s.handleIndex)
	mux.Handle("GET /admin/assets/", http.StripPrefix("/admin/", http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.FileServer(http.FS(staticSub)).ServeHTTP(rw, r)
	})))

	s.registerAPI(mux)
	return s.withAuth(mux)
}

// withAuth 在配置了 Token 时校验管理面访问。
//
// @remarks 支持 admin / operator / view 三种令牌；写操作需要 operator/admin，
// 并额外要求 X-Requested-With 头，降低 CSRF 风险。

func (s *Server) withAuth(next http.Handler) http.Handler {
	if s.cfg.Admin.Token == "" && s.cfg.Admin.OperatorToken == "" && s.cfg.Admin.ViewToken == "" {
		return next
	}
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/login" {
			s.handleLogin(rw, r)
			return
		}
		role, ok := s.authorized(r)
		if !ok {
			rw.Header().Set("WWW-Authenticate", `Bearer realm="agenthub"`)
			writeError(rw, http.StatusUnauthorized, "未授权")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if role == "viewer" {
				writeError(rw, http.StatusForbidden, "只读令牌不能执行写操作")
				return
			}
			if r.Header.Get("X-Requested-With") != "agenthub" {
				writeError(rw, http.StatusForbidden, "缺少 CSRF 头")
				return
			}
		}
		next.ServeHTTP(rw, r)
	})
}

func (s *Server) authorized(r *http.Request) (string, bool) {
	token := ""
	if header := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); header != "" && header != r.Header.Get("Authorization") {
		token = header
	}
	if token == "" {
		if cookie, err := r.Cookie("agenthub_admin"); err == nil {
			token = cookie.Value
		}
	}
	switch {
	case token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Admin.Token)) == 1:
		return "admin", true
	case token != "" && s.cfg.Admin.OperatorToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Admin.OperatorToken)) == 1:
		return "operator", true
	case token != "" && s.cfg.Admin.ViewToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Admin.ViewToken)) == 1:
		return "viewer", true
	default:
		return "", false
	}
}

func (s *Server) loginToken(token string) bool {
	for _, candidate := range []string{s.cfg.Admin.Token, s.cfg.Admin.OperatorToken, s.cfg.Admin.ViewToken} {
		if candidate != "" && subtle.ConstantTimeCompare([]byte(token), []byte(candidate)) == 1 {
			return true
		}
	}
	return false
}

func (s *Server) handleLogin(rw http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("token")
	if !s.loginToken(raw) {
		writeError(rw, http.StatusUnauthorized, "token 不正确")
		return
	}
	http.SetCookie(rw, &http.Cookie{
		Name:     "agenthub_admin",
		Value:    raw,
		Path:     "/admin",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(rw, r, "/admin/", http.StatusSeeOther)
}

// Run 启动管理监听。

func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Admin.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("admin listening", "addr", s.cfg.Admin.Addr)
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

func (s *Server) handleIndex(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/admin/" && r.URL.Path != "/admin" {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Cache-Control", "no-cache")
	data, err := fs.ReadFile(staticSub, "index.html")
	if err != nil {
		http.Error(rw, "missing index", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = rw.Write(data)
}

type modelView struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url"`
	HasKey   bool   `json:"has_key"`
	Echo     bool   `json:"echo"`
}

func (s *Server) audit(r *http.Request, action, target, detail string) {
	if s.opts.AuditRecord == nil {
		return
	}
	actor := ""
	if r != nil {
		actor = r.Header.Get("X-AA-Operator")
	}
	s.opts.AuditRecord(actor, action, target, detail)
}

func decode(r *http.Request, out any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return errors.New("请求体为空")
	}
	return json.Unmarshal(body, out)
}

func writeJSON(rw http.ResponseWriter, status int, body any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(body)
}

func writeError(rw http.ResponseWriter, status int, msg string) {
	writeJSON(rw, status, map[string]string{"error": msg})
}

func pageParams(r *http.Request, defSize int) (int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size <= 0 {
		size = defSize
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

// slicePage 返回当前页与总数。

func slicePage[T any](items []T, page, size int) ([]T, int) {
	total := len(items)
	start := (page - 1) * size
	if start >= total {
		return []T{}, total
	}
	end := start + size
	if end > total {
		end = total
	}
	return items[start:end], total
}

func queryTime(r *http.Request, key string) *time.Time {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if value, err := time.Parse(layout, raw); err == nil {
			return &value
		}
	}
	return nil
}

func queryInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
