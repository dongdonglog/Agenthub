package admin

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/scheduler"
	"github.com/moying/agenthub/internal/store"
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
	// ModelSettings 返回当前模型配置（含明文 Key，仅内部使用）。
	ModelSettings func() agent.ModelSettings
	// SetModelSettings 更新模型配置；为空表示不支持在线修改。
	SetModelSettings func(agent.ModelSettings) error
	// Journal 用于定时任务读写；为空表示不启用定时任务管理。
	Journal *store.Journal
	// Scheduler 用于立即执行定时任务。
	Scheduler *scheduler.Scheduler
	// ReloadAgentConfig 在 MCP/模型配置变更后重新生成 pi 配置。
	ReloadAgentConfig func() error
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
	mux.HandleFunc("PUT /admin/api/v1/sessions/{id}/profile", s.handleSessionProfile)
	mux.HandleFunc("GET /admin/api/v1/queue", s.handleQueue)
	mux.HandleFunc("GET /admin/api/v1/tasks", s.handleTasks)
	mux.HandleFunc("POST /admin/api/v1/tasks", s.handleTasks)
	mux.HandleFunc("GET /admin/api/v1/tasks/{id}", s.handleTask)
	mux.HandleFunc("PUT /admin/api/v1/tasks/{id}", s.handleTask)
	mux.HandleFunc("DELETE /admin/api/v1/tasks/{id}", s.handleTask)
	mux.HandleFunc("POST /admin/api/v1/tasks/{id}/run", s.handleTaskRun)
	mux.HandleFunc("PUT /admin/api/v1/sessions/{id}/priority", s.handleSessionPriority)
	mux.HandleFunc("GET /admin/api/v1/runs", s.handleRuns)
	mux.HandleFunc("GET /admin/api/v1/runs/{id}", s.handleRunDetail)

	mux.HandleFunc("GET /admin/api/v1/skills", s.handleSkills)
	mux.HandleFunc("GET /admin/api/v1/skills/{name}", s.handleSkill)
	mux.HandleFunc("PUT /admin/api/v1/skills/{name}", s.handleSkill)
	mux.HandleFunc("PUT /admin/api/v1/skills/{name}/status", s.handleSkillStatus)
	mux.HandleFunc("POST /admin/api/v1/skills/import", s.handleSkillImport)
	mux.HandleFunc("GET /admin/api/v1/skills/{name}/files", s.handleSkillFiles)
	mux.HandleFunc("GET /admin/api/v1/skills/{name}/files/{path...}", s.handleSkillFile)
	mux.HandleFunc("PUT /admin/api/v1/skills/{name}/files/{path...}", s.handleSkillFile)
	mux.HandleFunc("DELETE /admin/api/v1/skills/{name}/files/{path...}", s.handleSkillFile)
	mux.HandleFunc("DELETE /admin/api/v1/skills/{name}", s.handleSkill)

	mux.HandleFunc("GET /admin/api/v1/mcps", s.handleMCPs)
	mux.HandleFunc("PUT /admin/api/v1/mcps/{name}", s.handleMCP)
	mux.HandleFunc("DELETE /admin/api/v1/mcps/{name}", s.handleMCP)

	mux.HandleFunc("GET /admin/api/v1/settings", s.handleSettings)
	mux.HandleFunc("GET /admin/api/v1/model", s.handleModel)
	mux.HandleFunc("PUT /admin/api/v1/model", s.handleModel)
	mux.HandleFunc("GET /admin/api/v1/profiles", s.handleProfiles)
	mux.HandleFunc("PUT /admin/api/v1/profiles/{name}", s.handleProfile)
	mux.HandleFunc("DELETE /admin/api/v1/profiles/{name}", s.handleProfile)
	mux.HandleFunc("PUT /admin/api/v1/profiles/{name}/apply", s.handleProfileApply)
	mux.HandleFunc("GET /admin/api/v1/history", s.handleHistory)
	mux.HandleFunc("GET /admin/api/v1/history/{conversation}/{generation}", s.handleHistoryDetail)
	mux.HandleFunc("GET /admin/api/v1/audits", s.handleAudits)
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
	rw.Header().Set("Cache-Control", "no-cache")
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
		rw.Header().Set("Cache-Control", "no-cache")
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

func (s *Server) handleProfiles(rw http.ResponseWriter, _ *http.Request) {
	if s.opts.Profiles == nil {
		writeJSON(rw, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	items, err := s.opts.Profiles.ListProfiles()
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	active := s.opts.Profiles.Active()
	writeJSON(rw, http.StatusOK, map[string]any{"items": items, "total": len(items), "active": active.Name})
}

func (s *Server) handleProfile(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Profiles == nil {
		writeError(rw, http.StatusServiceUnavailable, "Profile 存储未接入")
		return
	}
	name := r.PathValue("name")
	switch r.Method {
	case http.MethodPut:
		var p store.Profile
		if err := decode(r, &p); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		p.Name = name
		if err := s.opts.Profiles.PutProfile(p); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.audit(r, "profile.save", name, "")
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok", "name": name})
	case http.MethodDelete:
		if err := s.opts.Profiles.DeleteProfile(name); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.audit(r, "profile.delete", name, "")
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeError(rw, http.StatusMethodNotAllowed, "方法不支持")
	}
}

func (s *Server) handleProfileApply(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Profiles == nil {
		writeError(rw, http.StatusServiceUnavailable, "Profile 存储未接入")
		return
	}
	name := r.PathValue("name")
	if err := s.opts.Profiles.Apply(name); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "profile.apply", name, "")
	writeJSON(rw, http.StatusOK, map[string]string{"status": "ok", "active": name})
}

func (s *Server) handleHistory(rw http.ResponseWriter, r *http.Request) {
	if s.opts.History == nil {
		writeJSON(rw, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	items, err := s.opts.History(r.Context())
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	page, size := pageParams(r, 20)
	pageItems, total := slicePage(items, page, size)
	writeJSON(rw, http.StatusOK, map[string]any{"items": pageItems, "total": total, "page": page, "size": size})
}

func (s *Server) handleHistoryDetail(rw http.ResponseWriter, r *http.Request) {
	if s.opts.HistoryMessages == nil {
		writeError(rw, http.StatusServiceUnavailable, "历史消息未接入")
		return
	}
	conv := r.PathValue("conversation")
	gen, err := strconv.Atoi(r.PathValue("generation"))
	if err != nil {
		writeError(rw, http.StatusBadRequest, "代必须是数字")
		return
	}
	items, err := s.opts.HistoryMessages(r.Context(), conv, gen)
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	page, size := pageParams(r, 20)
	pageItems, total := slicePage(items, page, size)
	writeJSON(rw, http.StatusOK, map[string]any{"items": pageItems, "total": total, "page": page, "size": size})
}

func (s *Server) handleAudits(rw http.ResponseWriter, r *http.Request) {
	if s.opts.AuditList == nil {
		writeJSON(rw, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	items, err := s.opts.AuditList(r.Context(), 200)
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	page, size := pageParams(r, 20)
	pageItems, total := slicePage(items, page, size)
	writeJSON(rw, http.StatusOK, map[string]any{"items": pageItems, "total": total, "page": page, "size": size})
}

func (s *Server) handleOverview(rw http.ResponseWriter, _ *http.Request) {
	skills, _ := s.store.ListSkills()
	mcps, _ := s.store.ListMCPs()
	convs := s.engine.Conversations()
	runs := s.engine.Runs()

	activeProfile := ""
	if s.opts.Profiles != nil {
		activeProfile = s.opts.Profiles.Active().Name
	}
	writeJSON(rw, http.StatusOK, map[string]any{
		"active_profile": activeProfile,
		"runtime":        s.opts.Runtime,
		"pi_version":     s.opts.PiVersion,
		"version":        s.opts.Version,
		"channel":        string(s.cfg.Channel),
		"transport":      s.cfg.Feishu.Transport,
		"feishu":         s.opts.FeishuStatus(),
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

func (s *Server) handleSessions(rw http.ResponseWriter, r *http.Request) {
	infos := s.engine.SessionInfos()
	page, size := pageParams(r, 20)
	items, total := slicePage(infos, page, size)
	writeJSON(rw, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "size": size})
}

// pageParams 解析 page/size，限制 size 上限。
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
		"profile":  s.engine.ProfileOf(id),
		"priority": priorityResponse(s.engine, id),
		"messages": messages,
		"runs":     runs,
	})
}

func (s *Server) handleTasks(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Journal == nil {
		writeError(rw, http.StatusServiceUnavailable, "定时任务需要数据库")
		return
	}
	if r.Method == http.MethodGet {
		items, err := s.opts.Journal.ListTasks(r.Context())
		if err != nil {
			writeError(rw, http.StatusInternalServerError, err.Error())
			return
		}
		if items == nil {
			items = []store.ScheduledTask{}
		}
		writeJSON(rw, http.StatusOK, map[string]any{"items": items, "total": len(items)})
		return
	}
	var body struct {
		Name     string `json:"name"`
		Cron     string `json:"cron"`
		Timezone string `json:"timezone"`
		ChatID   string `json:"chat_id"`
		Channel  string `json:"channel"`
		Prompt   string `json:"prompt"`
		Enabled  bool   `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	task, err := buildTask("", body.Name, body.Cron, body.Timezone, body.ChatID, body.Channel, body.Prompt, body.Enabled)
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.opts.Journal.SaveTask(r.Context(), task); err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "task.create", task.ID, task.Name)
	writeJSON(rw, http.StatusOK, task)
}

func (s *Server) handleTask(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Journal == nil {
		writeError(rw, http.StatusServiceUnavailable, "定时任务需要数据库")
		return
	}
	id := r.PathValue("id")
	switch r.Method {
	case http.MethodGet:
		task, err := s.opts.Journal.GetTask(r.Context(), id)
		if err != nil {
			writeError(rw, http.StatusNotFound, "任务不存在")
			return
		}
		writeJSON(rw, http.StatusOK, task)
	case http.MethodPut:
		var body struct {
			Name     string `json:"name"`
			Cron     string `json:"cron"`
			Timezone string `json:"timezone"`
			ChatID   string `json:"chat_id"`
			Channel  string `json:"channel"`
			Prompt   string `json:"prompt"`
			Enabled  bool   `json:"enabled"`
		}
		if err := decode(r, &body); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		task, err := buildTask(id, body.Name, body.Cron, body.Timezone, body.ChatID, body.Channel, body.Prompt, body.Enabled)
		if err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.opts.Journal.SaveTask(r.Context(), task); err != nil {
			writeError(rw, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r, "task.update", id, task.Name)
		writeJSON(rw, http.StatusOK, task)
	case http.MethodDelete:
		if err := s.opts.Journal.DeleteTask(r.Context(), id); err != nil {
			writeError(rw, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r, "task.delete", id, "")
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeError(rw, http.StatusMethodNotAllowed, "方法不支持")
	}
}

func (s *Server) handleTaskRun(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Journal == nil || s.opts.Scheduler == nil {
		writeError(rw, http.StatusServiceUnavailable, "调度器未启用")
		return
	}
	id := r.PathValue("id")
	task, err := s.opts.Journal.GetTask(r.Context(), id)
	if err != nil {
		writeError(rw, http.StatusNotFound, "任务不存在")
		return
	}
	if err := s.opts.Scheduler.RunNow(r.Context(), task); err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "task.run", id, "")
	writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
}

// buildTask 校验并构造任务；创建时生成 ID 并计算下次时间。
func buildTask(id, name, expr, tz, chatID, channelName, prompt string, enabled bool) (store.ScheduledTask, error) {
	if name == "" || chatID == "" || prompt == "" {
		return store.ScheduledTask{}, fmt.Errorf("name、chat_id、prompt 不能为空")
	}
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	if channelName == "" {
		channelName = "feishu"
	}
	now := time.Now()
	next, err := scheduler.NextRun(expr, tz, now)
	if err != nil {
		return store.ScheduledTask{}, err
	}
	if id == "" {
		id = fmt.Sprintf("task_%d", now.UnixNano())
	}
	task := store.ScheduledTask{
		ID: id, Name: name, Cron: expr, Timezone: tz, ChatID: chatID,
		Channel: channelName, Prompt: prompt, Enabled: enabled,
	}
	if enabled {
		task.NextRunAt = &next
	}
	return task, nil
}

func (s *Server) handleQueue(rw http.ResponseWriter, _ *http.Request) {
	items := s.engine.QueueSnapshot()
	writeJSON(rw, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleSessionPriority(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Priority int `json:"priority"`
	}
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.engine.SetPriority(r.Context(), id, body.Priority); err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "session.priority", id, fmt.Sprintf("%d", body.Priority))
	writeJSON(rw, http.StatusOK, map[string]any{"status": "ok", "priority": body.Priority})
}

func (s *Server) handleSessionProfile(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Profiles == nil {
		writeError(rw, http.StatusServiceUnavailable, "Profile 存储未接入")
		return
	}
	id := r.PathValue("id")
	var body struct {
		Name string `json:"name"`
	}
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.opts.Profiles.ListProfiles()
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	for _, p := range items {
		if p.Name == body.Name {
			if err := s.engine.RebindProfile(r.Context(), id, core.ProfileSelection{Name: p.Name, Skills: p.Skills, MCPs: p.MCPs}); err != nil {
				writeError(rw, http.StatusInternalServerError, err.Error())
				return
			}
			s.audit(r, "session.profile", id, p.Name)
			writeJSON(rw, http.StatusOK, map[string]string{"status": "ok", "profile": p.Name})
			return
		}
	}
	writeError(rw, http.StatusNotFound, "Profile 不存在")
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
	if s.opts.Profiles != nil {
		if profiles, err := s.opts.Profiles.ListProfiles(); err == nil {
			counts := make(map[string]int)
			for _, p := range profiles {
				for _, name := range p.Skills {
					counts[name]++
				}
			}
			for i := range items {
				items[i].References = counts[items[i].Name]
			}
		}
	}
	page, size := pageParams(r, 20)
	pageItems, total := slicePage(items, page, size)
	writeJSON(rw, http.StatusOK, map[string]any{"items": pageItems, "total": total, "page": page, "size": size})
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
		s.audit(r, "skill.save", name, "")
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok", "name": name})
	case http.MethodDelete:
		if err := s.store.DeleteSkill(name); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Info("skill deleted", "name", name)
		s.audit(r, "skill.delete", name, "")
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeError(rw, http.StatusMethodNotAllowed, "方法不支持")
	}
}

func (s *Server) handleSkillImport(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		writeError(rw, http.StatusBadRequest, "需要 multipart/form-data")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(rw, http.StatusBadRequest, "缺少 file 字段")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 20<<20))
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	overwrite := r.URL.Query().Get("overwrite") == "1"
	names, err := s.store.ImportZip(data, overwrite)
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "skill.import", strings.Join(names, ","), "")
	writeJSON(rw, http.StatusOK, map[string]any{"status": "ok", "imported": names})
}

func (s *Server) handleSkillFiles(rw http.ResponseWriter, r *http.Request) {
	files, err := s.store.ListFiles(r.PathValue("name"))
	if err != nil {
		writeError(rw, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"items": files, "total": len(files)})
}

func (s *Server) handleSkillFile(rw http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	rel := r.PathValue("path")
	switch r.Method {
	case http.MethodGet:
		content, err := s.store.ReadFile(name, rel)
		if err != nil {
			writeError(rw, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(rw, http.StatusOK, map[string]string{"path": rel, "content": content})
	case http.MethodPut:
		var body struct {
			Content string `json:"content"`
		}
		if err := decode(r, &body); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.store.WriteFile(name, rel, body.Content); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.audit(r, "skill.file.save", name, rel)
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
	case http.MethodDelete:
		if err := s.store.DeleteFile(name, rel); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.audit(r, "skill.file.delete", name, rel)
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeError(rw, http.StatusMethodNotAllowed, "方法不支持")
	}
}

func (s *Server) handleSkillStatus(rw http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Status string `json:"status"`
	}
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SetSkillStatus(name, body.Status); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("skill status changed", "name", name, "status", body.Status)
	s.audit(r, "skill.status", name, body.Status)
	writeJSON(rw, http.StatusOK, map[string]string{"status": "ok", "name": name})
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
	page, size := pageParams(r, 20)
	pageItems, total := slicePage(items, page, size)
	writeJSON(rw, http.StatusOK, map[string]any{"items": pageItems, "total": total, "page": page, "size": size})
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
		if s.opts.ReloadAgentConfig != nil {
			if err := s.opts.ReloadAgentConfig(); err != nil {
				s.log.Warn("reload agent config failed", "error", err)
			}
		}
		s.audit(r, "mcp.save", name, "")
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok", "name": name})
	case http.MethodDelete:
		if err := s.store.DeleteMCP(name); err != nil {
			writeError(rw, http.StatusBadRequest, err.Error())
			return
		}
		s.log.Info("mcp deleted", "name", name)
		if s.opts.ReloadAgentConfig != nil {
			if err := s.opts.ReloadAgentConfig(); err != nil {
				s.log.Warn("reload agent config failed", "error", err)
			}
		}
		s.audit(r, "mcp.delete", name, "")
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

func (s *Server) handleModel(rw http.ResponseWriter, r *http.Request) {
	if s.opts.ModelSettings == nil {
		writeError(rw, http.StatusServiceUnavailable, "模型配置未接入")
		return
	}
	current := s.opts.ModelSettings()
	if r.Method == http.MethodGet {
		writeJSON(rw, http.StatusOK, map[string]any{
			"base_url":       current.BaseURL,
			"model":          current.Model,
			"fallback_model": current.FallbackModel,
			"vision_model":   current.VisionModel,
			"has_key":        current.APIKey != "",
			"key_hint":       maskKey(current.APIKey),
		})
		return
	}
	if s.opts.SetModelSettings == nil {
		writeError(rw, http.StatusServiceUnavailable, "不支持在线修改模型配置")
		return
	}
	var body struct {
		BaseURL       string `json:"base_url"`
		APIKey        string `json:"api_key"`
		Model         string `json:"model"`
		FallbackModel string `json:"fallback_model"`
		VisionModel   string `json:"vision_model"`
		ClearKey      bool   `json:"clear_key"`
	}
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	next := agent.ModelSettings{
		BaseURL:       strings.TrimSpace(body.BaseURL),
		APIKey:        current.APIKey,
		Model:         strings.TrimSpace(body.Model),
		FallbackModel: strings.TrimSpace(body.FallbackModel),
		VisionModel:   strings.TrimSpace(body.VisionModel),
	}
	if next.BaseURL == "" || next.Model == "" {
		writeError(rw, http.StatusBadRequest, "base_url 与 model 不能为空")
		return
	}
	switch {
	case body.ClearKey:
		next.APIKey = ""
	case strings.TrimSpace(body.APIKey) != "":
		next.APIKey = strings.TrimSpace(body.APIKey)
	}
	if err := s.opts.SetModelSettings(next); err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "model.update", next.Model, "")
	writeJSON(rw, http.StatusOK, map[string]any{"status": "ok", "has_key": next.APIKey != ""})
}

func priorityResponse(e *core.Engine, id string) any {
	if p, ok := e.PriorityOf(id); ok {
		return p
	}
	return nil
}

func maskKey(key string) string {
	if len(key) <= 4 {
		return "****"
	}
	return "****" + key[len(key)-4:]
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
