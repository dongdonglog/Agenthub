package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/store"
)

func (s *Server) handleIncidents(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Incidents == nil {
		writeJSON(rw, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	limit := queryInt(r, "limit", 50)
	offset := queryInt(r, "offset", 0)
	items, total, err := s.opts.Incidents.ListIncidentsFiltered(r.Context(), store.IncidentFilter{
		Status: r.URL.Query().Get("status"), Service: r.URL.Query().Get("service"),
		Query: r.URL.Query().Get("q"), From: queryTime(r, "from"), To: queryTime(r, "to"),
		Limit: limit, Offset: offset,
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
