package admin

import (
	"fmt"
	"net/http"
	"time"

	"github.com/moying/agenthub/internal/scheduler"
	"github.com/moying/agenthub/internal/store"
)

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
