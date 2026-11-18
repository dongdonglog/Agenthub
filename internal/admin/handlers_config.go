package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/modelcfg"
	"github.com/moying/agenthub/internal/modelrouter"
	"github.com/moying/agenthub/internal/store"
)

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
