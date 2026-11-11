package admin

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/core"
)

//go:embed static/*
var staticFS embed.FS

// Options 是管理面的运行期信息。
type Options struct {
	// Version 是构建版本。
	Version string
	// FeishuStatus 返回通道连接状态：connected / reconnecting / connecting。
	FeishuStatus func() string
	// Agent 用于管理台的在线试跑。
	Agent agent.Agent
	// Tools 是当前可用工具，用于概览展示。
	Tools []agent.Tool
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
	mux.HandleFunc("GET /admin/app.js", s.handleAsset("static/app.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("GET /admin/styles.css", s.handleAsset("static/styles.css", "text/css; charset=utf-8"))

	mux.HandleFunc("GET /admin/api/v1/overview", s.handleOverview)
	mux.HandleFunc("GET /admin/api/v1/sessions", s.handleSessions)
	mux.HandleFunc("GET /admin/api/v1/sessions/{id}", s.handleSession)
	mux.HandleFunc("GET /admin/api/v1/runs", s.handleRuns)
	mux.HandleFunc("GET /admin/api/v1/runs/{id}", s.handleRunDetail)

	mux.HandleFunc("GET /admin/api/v1/skills", s.handleSkills)
	mux.HandleFunc("GET /admin/api/v1/skills/{name}", s.handleSkill)
	mux.HandleFunc("PUT /admin/api/v1/skills/{name}", s.handleSkill)
	mux.HandleFunc("DELETE /admin/api/v1/skills/{name}", s.handleSkill)

	mux.HandleFunc("GET /admin/api/v1/mcps", s.handleMCPs)
	mux.HandleFunc("PUT /admin/api/v1/mcps/{name}", s.handleMCP)
	mux.HandleFunc("DELETE /admin/api/v1/mcps/{name}", s.handleMCP)

	mux.HandleFunc("GET /admin/api/v1/settings", s.handleSettings)
	mux.HandleFunc("POST /admin/api/v1/test", s.handleTest)
	return s.withAuth(mux)
}

// withAuth 在配置了 Token 时校验管理面访问。
//
// @remarks 支持 Bearer 头与登录 Cookie；写操作额外要求 X-Requested-With 头，降低 CSRF 风险。
func (s *Server) withAuth(next http.Handler) http.Handler {
	token := s.cfg.Admin.Token
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if token == "" {
			next.ServeHTTP(rw, r)
			return
		}
		if r.URL.Path == "/admin/login" {
			s.handleLogin(rw, r)
			return
		}
		if !s.authorized(r, token) {
			rw.Header().Set("WWW-Authenticate", `Bearer realm="agenthub"`)
			writeError(rw, http.StatusUnauthorized, "未授权")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get("X-Requested-With") != "agenthub" {
				writeError(rw, http.StatusForbidden, "缺少 CSRF 头")
				return
			}
		}
		next.ServeHTTP(rw, r)
	})
}

func (s *Server) authorized(r *http.Request, token string) bool {
	if r.Header.Get("Authorization") == "Bearer "+token {
		return true
	}
	if cookie, err := r.Cookie("agenthub_admin"); err == nil && cookie.Value == token {
		return true
	}
	return false
}

func (s *Server) handleLogin(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("token") != s.cfg.Admin.Token {
		writeError(rw, http.StatusUnauthorized, "token 不正确")
		return
	}
	http.SetCookie(rw, &http.Cookie{
		Name:     "agenthub_admin",
		Value:    s.cfg.Admin.Token,
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
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(rw, "missing index", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = rw.Write(data)
}

func (s *Server) handleAsset(name, contentType string) http.HandlerFunc {
	return func(rw http.ResponseWriter, _ *http.Request) {
		data, err := staticFS.ReadFile(name)
		if err != nil {
			http.Error(rw, "not found", http.StatusNotFound)
			return
		}
		rw.Header().Set("Content-Type", contentType)
		_, _ = rw.Write(data)
	}
}

type modelView struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url"`
	HasKey   bool   `json:"has_key"`
	Echo     bool   `json:"echo"`
}

func (s *Server) handleOverview(rw http.ResponseWriter, _ *http.Request) {
	skills, _ := s.store.ListSkills()
	mcps, _ := s.store.ListMCPs()
	convs := s.engine.Conversations()
	runs := s.engine.Runs()

	writeJSON(rw, http.StatusOK, map[string]any{
		"version":   s.opts.Version,
		"channel":   string(s.cfg.Channel),
		"transport": s.cfg.Feishu.Transport,
		"feishu":    s.opts.FeishuStatus(),
		"model": modelView{
			Provider: s.cfg.Agent.Provider,
			Model:    s.cfg.Agent.Model,
			BaseURL:  s.cfg.Agent.BaseURL,
			HasKey:   s.cfg.Agent.APIKey != "",
			Echo:     s.cfg.Agent.APIKey == "",
		},
		"counts": map[string]int{
			"conversations": len(convs),
			"runs":          len(runs),
			"skills":        len(skills),
			"mcps":          len(mcps),
			"tools":         len(s.opts.Tools),
		},
	})
}

func (s *Server) handleSessions(rw http.ResponseWriter, _ *http.Request) {
	convs := s.engine.Conversations()
	writeJSON(rw, http.StatusOK, map[string]any{"items": convs, "total": len(convs)})
}

func (s *Server) handleSession(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var messages []*core.Message
	for _, m := range s.engine.Messages(id) {
		messages = append(messages, m)
	}
	if len(messages) == 0 {
		writeError(rw, http.StatusNotFound, "会话不存在或已过期（当前为内存存储）")
		return
	}
	var runs []*core.Run
	for _, run := range s.engine.Runs() {
		if run.ConversationID == id {
			runs = append(runs, run)
		}
	}
	writeJSON(rw, http.StatusOK, map[string]any{
		"id":       id,
		"messages": messages,
		"runs":     runs,
	})
}

func (s *Server) handleRuns(rw http.ResponseWriter, _ *http.Request) {
	runs := s.engine.Runs()
	writeJSON(rw, http.StatusOK, map[string]any{"items": runs, "total": len(runs)})
}

func (s *Server) handleRunDetail(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run := s.engine.RunByID(id)
	if run == nil {
		writeError(rw, http.StatusNotFound, "运行不存在或已过期")
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{
		"run":        run,
		"events":     s.engine.RunEvents(id),
		"deliveries": s.engine.DeliveriesFor(id),
	})
}

func (s *Server) handleSkills(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(rw, http.StatusMethodNotAllowed, "方法不支持")
		return
	}
	items, err := s.store.ListSkills()
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleSkill(rw http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	switch r.Method {
	case http.MethodGet:
		item, err := s.store.GetSkill(name)
		if err != nil {
			writeError(rw, http.StatusNotFound, "技能不存在")
			return
		}
		writeJSON(rw, http.StatusOK, item)
	case http.MethodPut:
		var body struct {
			Content string `json:"content"`
		}
		if err := decode(r, &body); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		if strings.TrimSpace(body.Content) == "" {
			writeError(rw, http.StatusBadRequest, "内容不能为空")
			return
		}
		if err := s.store.PutSkill(name, body.Content); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Info("skill saved", "name", name)
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok", "name": name})
	case http.MethodDelete:
		if err := s.store.DeleteSkill(name); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Info("skill deleted", "name", name)
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeError(rw, http.StatusMethodNotAllowed, "方法不支持")
	}
}

func (s *Server) handleMCPs(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(rw, http.StatusMethodNotAllowed, "方法不支持")
		return
	}
	items, err := s.store.ListMCPs()
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleMCP(rw http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	switch r.Method {
	case http.MethodPut:
		var item MCP
		if err := decode(r, &item); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		item.Name = name
		if item.Transport == "" {
			item.Transport = "stdio"
		}
		if err := s.store.PutMCP(item); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Info("mcp saved", "name", name)
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok", "name": name})
	case http.MethodDelete:
		if err := s.store.DeleteMCP(name); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Info("mcp deleted", "name", name)
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeError(rw, http.StatusMethodNotAllowed, "方法不支持")
	}
}

func (s *Server) handleTest(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Agent == nil {
		writeError(rw, http.StatusServiceUnavailable, "执行器未就绪")
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeError(rw, http.StatusBadRequest, "内容不能为空")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	start := time.Now()
	out, err := s.opts.Agent.Run(ctx, agent.Input{
		ConversationID: "admin-test",
		Messages:       []agent.Message{{ID: "admin-test", Text: body.Text}},
	})
	if err != nil {
		writeError(rw, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{
		"output":     out.Text,
		"latency_ms": time.Since(start).Milliseconds(),
	})
}

func (s *Server) handleSettings(rw http.ResponseWriter, _ *http.Request) {
	writeJSON(rw, http.StatusOK, map[string]any{
		"model": map[string]any{
			"provider": s.cfg.Agent.Provider,
			"model":    s.cfg.Agent.Model,
			"base_url": s.cfg.Agent.BaseURL,
			"has_key":  s.cfg.Agent.APIKey != "",
			"note":     "修改 .env 后重启生效",
		},
		"feishu": map[string]any{
			"transport":     s.cfg.Feishu.Transport,
			"allowed_users": s.cfg.Feishu.AllowedUsers,
			"allowed_chats": s.cfg.Feishu.AllowedChats,
			"require_at":    s.cfg.Feishu.RequireMention,
		},
		"storage": map[string]string{"data_dir": s.cfg.Storage.Dir},
	})
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
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(body)
}

func writeError(rw http.ResponseWriter, status int, msg string) {
	writeJSON(rw, status, map[string]string{"error": msg})
}
