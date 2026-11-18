package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/moying/agenthub/internal/core"
)

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

func (s *Server) handleDeliveryRetry(rw http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if err := s.engine.RetryDelivery(r.Context(), key); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "delivery.retry", key, "")
	writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleQueue(rw http.ResponseWriter, _ *http.Request) {
	items := s.engine.QueueSnapshot()
	writeJSON(rw, http.StatusOK, map[string]any{"items": items, "total": len(items)})
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
