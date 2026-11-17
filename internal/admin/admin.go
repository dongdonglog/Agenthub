package admin

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
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

	mux.HandleFunc("GET /admin/api/v1/overview", s.handleOverview)
	mux.HandleFunc("GET /admin/api/v1/sessions", s.handleSessions)
	mux.HandleFunc("GET /admin/api/v1/sessions/{id}", s.handleSession)
	mux.HandleFunc("PUT /admin/api/v1/sessions/{id}/profile", s.handleSessionProfile)
	mux.HandleFunc("POST /admin/api/v1/sessions/{id}/apply-config", s.handleSessionApplyConfig)
	mux.HandleFunc("POST /admin/api/v1/sessions/apply-config", s.handleSessionsApplyConfig)
	mux.HandleFunc("POST /admin/api/v1/sessions/{id}/reset", s.handleSessionReset)
	mux.HandleFunc("POST /admin/api/v1/sessions/reset", s.handleSessionsReset)
	mux.HandleFunc("POST /admin/api/v1/deliveries/{key}/retry", s.handleDeliveryRetry)
	mux.HandleFunc("GET /admin/api/v1/employees", s.handleEmployees)
	mux.HandleFunc("GET /admin/api/v1/cases", s.handleCases)
	mux.HandleFunc("GET /admin/api/v1/cases/{id}", s.handleCase)
	mux.HandleFunc("POST /admin/api/v1/cases/{id}/handoff", s.handleCaseHandoff)
	mux.HandleFunc("POST /admin/api/v1/cases/{id}/resolve", s.handleCaseResolve)
	mux.HandleFunc("POST /admin/api/v1/cases/{id}/postmortem", s.handleCasePostmortem)
	mux.HandleFunc("POST /admin/api/v1/cases/{id}/feedback", s.handleCaseFeedback)
	mux.HandleFunc("POST /admin/api/v1/cases", s.handleCaseCreate)
	mux.HandleFunc("GET /admin/api/v1/employees/{id}/stats", s.handleEmployeeStats)
	mux.HandleFunc("PUT /admin/api/v1/employees/{id}", s.handleEmployeeUpdate)
	mux.HandleFunc("POST /admin/api/v1/cases/{id}/approvals", s.handleCaseApprovalCreate)
	mux.HandleFunc("POST /admin/api/v1/approvals/{id}/approve", s.handleApprovalDecide)
	mux.HandleFunc("POST /admin/api/v1/approvals/{id}/reject", s.handleApprovalDecide)
	mux.HandleFunc("GET /admin/api/v1/incidents", s.handleIncidents)
	mux.HandleFunc("GET /admin/api/v1/incidents/{id}", s.handleIncident)
	mux.HandleFunc("POST /admin/api/v1/incidents/{id}/ack", s.handleIncidentAck)
	mux.HandleFunc("POST /admin/api/v1/incidents/{id}/resolve", s.handleIncidentResolve)
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
	mux.HandleFunc("GET /admin/api/v1/artifacts/{id}", s.handleArtifact)

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
	mux.HandleFunc("POST /admin/api/v1/model/test", s.handleModelTest)
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
	messageIDs := make(map[string]bool, len(items))
	for _, m := range items {
		messageIDs[m.ID] = true
	}
	runs := make([]map[string]any, 0)
	for _, run := range s.engine.Runs() {
		if run.ConversationID != conv {
			continue
		}
		matched := false
		for _, id := range run.InputIDs {
			if messageIDs[id] {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		runs = append(runs, map[string]any{"run": run, "events": s.engine.RunEvents(run.ID)})
	}
	writeJSON(rw, http.StatusOK, map[string]any{
		"items": pageItems, "total": total, "page": page, "size": size, "runs": runs,
	})
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
	userSessions := 0
	for _, info := range s.engine.SessionInfos() {
		if !info.Internal {
			userSessions++
		}
	}
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
			"conversations": userSessions,
			"runs":          len(runs),
			"skills":        len(skills),
			"mcps":          len(mcps),
			"tools":         len(s.opts.Tools),
		},
	})
}

func (s *Server) handleSessions(rw http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "user"
	}
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	includeInternal := r.URL.Query().Get("include_internal") == "1"

	infos := s.engine.SessionInfos()
	filtered := make([]core.SessionInfo, 0, len(infos))
	for _, info := range infos {
		if !includeInternal && info.Internal {
			continue
		}
		switch kind {
		case "all":
		case "user":
			if info.Kind == "scheduled" {
				continue
			}
		default:
			if info.Kind != kind {
				continue
			}
		}
		if query != "" && !strings.Contains(strings.ToLower(info.DisplayName), query) &&
			!strings.Contains(strings.ToLower(info.ID), query) {
			continue
		}
		filtered = append(filtered, info)
	}

	page, size := pageParams(r, 20)
	items, total := slicePage(filtered, page, size)
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

// handleSessionApplyConfig 把当前生效 Profile 和部署配置应用到单个会话当前代。
func (s *Server) handleSessionApplyConfig(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Profiles == nil {
		writeError(rw, http.StatusServiceUnavailable, "Profile 存储未接入")
		return
	}
	id := r.PathValue("id")
	active := s.opts.Profiles.Active()
	selection := core.ProfileSelection{Name: active.Name, Skills: active.Skills, MCPs: active.MCPs}
	if s.opts.ReloadAgentConfig != nil {
		if err := s.opts.ReloadAgentConfig(); err != nil {
			writeError(rw, http.StatusInternalServerError, "同步工作区配置失败: "+err.Error())
			return
		}
	}
	if err := s.engine.RebindProfile(r.Context(), id, selection); err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "session.apply_config", id, selection.Name)
	writeJSON(rw, http.StatusOK, map[string]any{"status": "ok", "profile": selection.Name})
}

// handleSessionsApplyConfig 把当前生效 Profile 应用到所有已有会话的当前代。
func (s *Server) handleSessionsApplyConfig(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Profiles == nil {
		writeError(rw, http.StatusServiceUnavailable, "Profile 存储未接入")
		return
	}
	active := s.opts.Profiles.Active()
	selection := core.ProfileSelection{Name: active.Name, Skills: active.Skills, MCPs: active.MCPs}
	if s.opts.ReloadAgentConfig != nil {
		if err := s.opts.ReloadAgentConfig(); err != nil {
			writeError(rw, http.StatusInternalServerError, "同步工作区配置失败: "+err.Error())
			return
		}
	}
	count, err := s.engine.ApplyProfileToCurrentGenerations(r.Context(), selection)
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "sessions.apply_config", "all", fmt.Sprintf("profile=%s sessions=%d", selection.Name, count))
	writeJSON(rw, http.StatusOK, map[string]any{"status": "ok", "profile": selection.Name, "sessions": count})
}

func (s *Server) handleSessionReset(rw http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stopped := s.engine.Reset(id)
	s.audit(r, "session.reset", id, "")
	writeJSON(rw, http.StatusOK, map[string]any{"status": "ok", "stopped": stopped, "generation": s.engine.Generation(id)})
}

func (s *Server) handleSessionsReset(rw http.ResponseWriter, r *http.Request) {
	count := 0
	for _, info := range s.engine.SessionInfos() {
		if info.Internal {
			continue
		}
		s.engine.Reset(info.ID)
		count++
	}
	s.audit(r, "sessions.reset", "all", fmt.Sprintf("sessions=%d", count))
	writeJSON(rw, http.StatusOK, map[string]any{"status": "ok", "sessions": count})
}

func (s *Server) handleDeliveryRetry(rw http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if err := s.engine.RetryDelivery(r.Context(), key); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "delivery.retry", key, "")
	writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleEmployees(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeJSON(rw, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	items, err := s.opts.Cases.ListEmployees(r.Context())
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*store.Employee{}
	}
	writeJSON(rw, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleCases(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeJSON(rw, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	limit := queryInt(r, "limit", 50)
	offset := queryInt(r, "offset", 0)
	items, total, err := s.opts.Cases.ListCasesFiltered(r.Context(), store.CaseFilter{
		Status: r.URL.Query().Get("status"), Type: r.URL.Query().Get("type"),
		Source: r.URL.Query().Get("source"), Query: r.URL.Query().Get("q"),
		Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*store.Case{}
	}
	writeJSON(rw, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleCase(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeError(rw, http.StatusServiceUnavailable, "案件存储未接入")
		return
	}
	c, err := s.opts.Cases.GetCase(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	if c == nil {
		writeError(rw, http.StatusNotFound, "案件不存在")
		return
	}
	events, err := s.opts.Cases.ListCaseEvents(r.Context(), c.ID, 300)
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	feedback, _ := s.opts.Cases.ListFeedback(r.Context(), c.ID, 50)
	approvals, _ := s.opts.Cases.ListApprovals(r.Context(), c.ID)
	var run any
	if s.opts.Journal != nil {
		conv := ""
		switch {
		case c.Source == "alertmanager":
			conv = "aiops:" + c.SourceID
		case c.Type == "ticket":
			conv = "support:" + c.SourceID
		}
		if conv != "" {
			run, _ = s.opts.Journal.LatestRunForConversation(r.Context(), conv)
		}
	}
	writeJSON(rw, http.StatusOK, map[string]any{"case": c, "events": events, "feedback": feedback, "approvals": approvals, "run": run})
}

func (s *Server) handleCaseHandoff(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeError(rw, http.StatusServiceUnavailable, "案件存储未接入")
		return
	}
	c, err := s.opts.Cases.GetCase(r.Context(), r.PathValue("id"))
	if err != nil || c == nil {
		writeError(rw, http.StatusNotFound, "案件不存在")
		return
	}
	events, _ := s.opts.Cases.ListCaseEvents(r.Context(), c.ID, 50)
	var b strings.Builder
	fmt.Fprintf(&b, "【交接】%s\n状态: %s / %s\n服务: %s\n摘要: %s\n\n未闭环事项:\n", c.ID, c.Status, c.Severity, c.Service, c.Summary)
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == "resolved" {
			continue
		}
		fmt.Fprintf(&b, "- %s %s: %s\n", events[i].CreatedAt.Format("15:04"), events[i].Kind, events[i].Text)
	}
	text := b.String()
	_ = s.opts.Cases.AddCaseEvent(r.Context(), c.ID, "handoff", "admin", text, time.Now())
	s.audit(r, "case.handoff", c.ID, "")
	writeJSON(rw, http.StatusOK, map[string]any{"status": "ok", "summary": text})
}

func (s *Server) handleCaseResolve(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeError(rw, http.StatusServiceUnavailable, "案件存储未接入")
		return
	}
	id := r.PathValue("id")
	if err := s.opts.Cases.SetCaseStatus(r.Context(), id, "resolved", time.Now()); err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.opts.Cases.AddCaseEvent(r.Context(), id, "resolved", "admin", "人工关闭", time.Now())
	s.audit(r, "case.resolve", id, "")
	writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleCasePostmortem(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeError(rw, http.StatusServiceUnavailable, "案件存储未接入")
		return
	}
	c, err := s.opts.Cases.GetCase(r.Context(), r.PathValue("id"))
	if err != nil || c == nil {
		writeError(rw, http.StatusNotFound, "案件不存在")
		return
	}
	events, _ := s.opts.Cases.ListCaseEvents(r.Context(), c.ID, 100)
	var b strings.Builder
	fmt.Fprintf(&b, "# 事故复盘草稿：%s\n\n", c.Title)
	fmt.Fprintf(&b, "- 案件：%s\n- 服务：%s / %s\n- 级别：%s\n- 状态：%s\n\n", c.ID, c.Service, c.Environment, c.Severity, c.Status)
	b.WriteString("## 摘要\n\n")
	b.WriteString(c.Summary + "\n\n## 时间线\n\n")
	for i := len(events) - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "- %s [%s] %s: %s\n", events[i].CreatedAt.Format(time.RFC3339), events[i].Kind, events[i].Actor, events[i].Text)
	}
	b.WriteString("\n## 根因候选\n\n- 待补充（需结合指标/日志/变更证据）\n\n## 行动项\n\n- [ ] 补充根因\n- [ ] 补充改进项与负责人\n")
	text := b.String()
	_ = s.opts.Cases.AddCaseEvent(r.Context(), c.ID, "postmortem", "admin", "生成复盘草稿", time.Now())
	s.audit(r, "case.postmortem", c.ID, "")
	writeJSON(rw, http.StatusOK, map[string]any{"status": "ok", "markdown": text})
}

func (s *Server) handleCaseCreate(rw http.ResponseWriter, r *http.Request) {
	if s.opts.CreateTicket == nil {
		writeError(rw, http.StatusServiceUnavailable, "工单入口未启用")
		return
	}
	var body struct {
		Title       string `json:"title"`
		Summary     string `json:"summary"`
		Service     string `json:"service"`
		Environment string `json:"environment"`
		Priority    string `json:"priority"`
		Requester   string `json:"requester"`
	}
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.opts.CreateTicket(r.Context(), body.Title, body.Summary, body.Service, body.Environment, body.Priority, body.Requester)
	if err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "case.create", id, body.Title)
	writeJSON(rw, http.StatusOK, map[string]string{"id": id})
}

func (s *Server) handleCaseFeedback(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeError(rw, http.StatusServiceUnavailable, "案件存储未接入")
		return
	}
	var body struct {
		Verdict string `json:"verdict"`
		Note    string `json:"note"`
		Actor   string `json:"actor"`
		CSAT    int    `json:"csat"`
	}
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	if body.Verdict != "useful" && body.Verdict != "not_useful" {
		writeError(rw, http.StatusBadRequest, "verdict 必须是 useful 或 not_useful")
		return
	}
	if body.Actor == "" {
		body.Actor = "admin"
	}
	id := r.PathValue("id")
	if body.CSAT < 0 || body.CSAT > 5 {
		writeError(rw, http.StatusBadRequest, "csat 必须在 0-5")
		return
	}
	if err := s.opts.Cases.AddFeedback(r.Context(), id, body.Actor, body.Verdict, body.Note, body.CSAT, time.Now()); err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.opts.Cases.AddCaseEvent(r.Context(), id, "feedback", body.Actor, body.Verdict+" "+body.Note, time.Now())
	s.audit(r, "case.feedback", id, body.Verdict)
	writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleCaseApprovalCreate(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeError(rw, http.StatusServiceUnavailable, "案件存储未接入")
		return
	}
	var body struct {
		Action string          `json:"action"`
		Params json.RawMessage `json:"params"`
		Actor  string          `json:"actor"`
	}
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	if body.Action != "k8s_scale" && body.Action != "k8s_restart" {
		writeError(rw, http.StatusBadRequest, "只支持 k8s_scale / k8s_restart")
		return
	}
	if body.Actor == "" {
		body.Actor = "admin"
	}
	id := r.PathValue("id")
	a, err := s.opts.Cases.CreateApproval(r.Context(), id, body.Action, body.Params, body.Actor, time.Now())
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.opts.Cases.AddCaseEvent(r.Context(), id, "approval", body.Actor, "申请动作 "+body.Action, time.Now())
	if s.opts.NotifyApproval != nil {
		if err := s.opts.NotifyApproval(r.Context(), a); err != nil {
			s.log.Warn("notify approval failed", "approval", a.ID, "error", err)
		}
	}
	s.audit(r, "approval.create", a.ID, body.Action)
	writeJSON(rw, http.StatusOK, a)
}

func (s *Server) handleApprovalDecide(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeError(rw, http.StatusServiceUnavailable, "案件存储未接入")
		return
	}
	id := r.PathValue("id")
	status := "approved"
	if strings.HasSuffix(r.URL.Path, "/reject") {
		status = "rejected"
	}
	var body struct {
		Actor string `json:"actor"`
	}
	_ = decode(r, &body)
	if body.Actor == "" {
		body.Actor = "admin"
	}
	a, ok, err := s.opts.Cases.DecideApproval(r.Context(), id, status, body.Actor, "", time.Now())
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(rw, http.StatusConflict, "审批不存在或已处理")
		return
	}
	if status == "approved" && s.opts.ExecuteAction != nil {
		result, execErr := s.opts.ExecuteAction(r.Context(), a)
		if execErr != nil {
			result = "执行失败: " + execErr.Error()
		}
		_ = s.opts.Cases.SetApprovalResult(r.Context(), a.ID, result, time.Now())
		_ = s.opts.Cases.AddCaseEvent(r.Context(), a.CaseID, "action", body.Actor, result, time.Now())
	}
	s.audit(r, "approval."+status, id, body.Actor)
	writeJSON(rw, http.StatusOK, map[string]string{"status": status})
}

func (s *Server) handleEmployeeUpdate(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeError(rw, http.StatusServiceUnavailable, "案件存储未接入")
		return
	}
	var body store.Employee
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	body.ID = r.PathValue("id")
	if body.ID == "" {
		writeError(rw, http.StatusBadRequest, "员工 ID 不能为空")
		return
	}
	emp, err := s.opts.Cases.EnsureEmployee(r.Context(), body, time.Now())
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "employee.update", emp.ID, strings.Join(emp.AllowedTools, ","))
	writeJSON(rw, http.StatusOK, emp)
}

func (s *Server) handleEmployeeStats(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeError(rw, http.StatusServiceUnavailable, "案件存储未接入")
		return
	}
	stats, err := s.opts.Cases.EmployeeStats(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(rw, http.StatusOK, stats)
}

func (s *Server) handleIncidents(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Incidents == nil {
		writeJSON(rw, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	limit := queryInt(r, "limit", 50)
	offset := queryInt(r, "offset", 0)
	items, total, err := s.opts.Incidents.ListIncidentsFiltered(r.Context(), store.IncidentFilter{
		Status: r.URL.Query().Get("status"), Service: r.URL.Query().Get("service"),
		Query: r.URL.Query().Get("q"), Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*store.Incident{}
	}
	writeJSON(rw, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleIncident(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Incidents == nil {
		writeError(rw, http.StatusServiceUnavailable, "AIOps 未接入")
		return
	}
	inc, err := s.opts.Incidents.GetIncident(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	if inc == nil {
		writeError(rw, http.StatusNotFound, "事故不存在")
		return
	}
	events, err := s.opts.Incidents.ListEvents(r.Context(), inc.ID, 200)
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	var linked *store.Case
	if s.opts.Cases != nil {
		linked, _ = s.opts.Cases.FindCaseBySource(r.Context(), "alertmanager", inc.ID)
	}
	var run any
	if linked != nil && s.opts.Journal != nil {
		run, _ = s.opts.Journal.LatestRunForConversation(r.Context(), "aiops:"+inc.ID)
	}
	writeJSON(rw, http.StatusOK, map[string]any{"incident": inc, "events": events, "case": linked, "run": run})
}

func (s *Server) handleIncidentAck(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Incidents == nil {
		writeError(rw, http.StatusServiceUnavailable, "AIOps 未接入")
		return
	}
	var body struct {
		Actor string `json:"actor"`
	}
	_ = decode(r, &body)
	if body.Actor == "" {
		body.Actor = "admin"
	}
	id := r.PathValue("id")
	if err := s.opts.Incidents.Acknowledge(r.Context(), id, body.Actor, time.Now()); err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.opts.Incidents.AddEvent(r.Context(), id, "acknowledged", body.Actor, "管理台认领", time.Now())
	s.audit(r, "incident.ack", id, body.Actor)
	writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleIncidentResolve(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Incidents == nil {
		writeError(rw, http.StatusServiceUnavailable, "AIOps 未接入")
		return
	}
	var body struct {
		Actor string `json:"actor"`
	}
	_ = decode(r, &body)
	if body.Actor == "" {
		body.Actor = "admin"
	}
	id := r.PathValue("id")
	inc, ok, err := s.opts.Incidents.ResolveIncidentByID(r.Context(), id, body.Actor, time.Now())
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(rw, http.StatusConflict, "事故不存在或已恢复")
		return
	}
	if s.opts.Cases != nil {
		if c, _ := s.opts.Cases.FindCaseBySource(r.Context(), "alertmanager", id); c != nil {
			_ = s.opts.Cases.SetCaseStatus(r.Context(), c.ID, "resolved", time.Now())
			_ = s.opts.Cases.AddCaseEvent(r.Context(), c.ID, "resolved", body.Actor, "事故手动恢复，案件关闭", time.Now())
		}
	}
	s.audit(r, "incident.resolve", inc.ID, body.Actor)
	writeJSON(rw, http.StatusOK, inc)
}

func queryInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || value < 0 {
		return fallback
	}
	return value
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
	artifacts := s.engine.Artifacts(r.Context(), id)
	if artifacts == nil {
		artifacts = []*core.Artifact{}
	}
	writeJSON(rw, http.StatusOK, map[string]any{
		"run":        run,
		"events":     s.engine.RunEvents(id),
		"deliveries": s.engine.DeliveriesFor(id),
		"artifacts":  artifacts,
	})
}

func (s *Server) handleArtifact(rw http.ResponseWriter, r *http.Request) {
	artifact := s.engine.Artifact(r.Context(), r.PathValue("id"))
	if artifact == nil {
		writeError(rw, http.StatusNotFound, "产物不存在")
		return
	}
	file, err := os.Open(artifact.Path)
	if err != nil {
		writeError(rw, http.StatusNotFound, "产物文件不存在")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(rw, http.StatusNotFound, "产物文件不存在")
		return
	}
	rw.Header().Set("Content-Type", "application/octet-stream")
	rw.Header().Set("Content-Disposition", contentDisposition(artifact.Name))
	http.ServeContent(rw, r, artifact.Name, info.ModTime(), file)
}

func contentDisposition(name string) string {
	name = strings.ReplaceAll(name, `"`, "")
	return "attachment; filename*=UTF-8''" + url.PathEscape(name)
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
	if s.opts.ModelConfig == nil {
		writeError(rw, http.StatusServiceUnavailable, "模型配置未接入")
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(rw, http.StatusOK, map[string]any{
			"config": maskModelConfig(s.opts.ModelConfig()),
			"health": healthOrEmpty(s.opts.ModelHealth),
		})
		return
	}
	if s.opts.SetModelConfig == nil {
		writeError(rw, http.StatusServiceUnavailable, "不支持在线修改模型配置")
		return
	}
	var body modelcfg.Config
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	current := s.opts.ModelConfig()
	keys := map[string]string{}
	for _, provider := range current.Providers {
		keys[provider.Name] = provider.APIKey
	}
	for i := range body.Providers {
		if key := strings.TrimSpace(body.Providers[i].APIKey); key != "" {
			keys[body.Providers[i].Name] = key
		} else {
			body.Providers[i].APIKey = keys[body.Providers[i].Name]
		}
	}
	if err := body.Validate(); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.opts.SetModelConfig(body); err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "model.update", "routes", "")
	writeJSON(rw, http.StatusOK, map[string]any{"status": "ok", "config": maskModelConfig(body)})
}

func (s *Server) handleModelTest(rw http.ResponseWriter, r *http.Request) {
	if s.opts.TestModel == nil {
		writeError(rw, http.StatusServiceUnavailable, "模型测试未接入")
		return
	}
	var body struct {
		Route string `json:"route"`
	}
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	if body.Route == "" {
		body.Route = "main"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := s.opts.TestModel(ctx, body.Route); err != nil {
		writeError(rw, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"status": "ok", "route": body.Route})
}

func healthOrEmpty(fn func() []modelrouter.CandidateHealth) []modelrouter.CandidateHealth {
	if fn == nil {
		return nil
	}
	return fn()
}

func maskModelConfig(cfg modelcfg.Config) modelcfg.Config {
	out := cfg
	out.Providers = make([]modelcfg.Provider, len(cfg.Providers))
	for i, provider := range cfg.Providers {
		provider.APIKey = ""
		out.Providers[i] = provider
	}
	return out
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
	rw.Header().Set("Cache-Control", "no-store")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(body)
}

func writeError(rw http.ResponseWriter, status int, msg string) {
	writeJSON(rw, status, map[string]string{"error": msg})
}
