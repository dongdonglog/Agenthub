package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/store"
)

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
		From: queryTime(r, "from"), To: queryTime(r, "to"),
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
	reviews, _ := s.opts.Cases.ListReviews(r.Context(), c.ID)
	report, _ := s.opts.Cases.GetCaseReport(r.Context(), c.ID)
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
	// 告警来源的案件把事故细节一并带上：事故是告警聚合事实，案件是工作项，
	// 对使用者而言应在一个页面里看完，不必在「事故」和「案件」之间来回跳。
	var incident any
	if c.Source == "alertmanager" && s.opts.Incidents != nil {
		if inc, _ := s.opts.Incidents.GetIncident(r.Context(), c.SourceID); inc != nil {
			incident = inc
		}
	}
	writeJSON(rw, http.StatusOK, map[string]any{
		"case": c, "events": events, "feedback": feedback, "reviews": reviews,
		"report": report, "approvals": approvals, "run": run, "incident": incident,
	})
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

func (s *Server) handleCaseReportGet(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeError(rw, http.StatusServiceUnavailable, "案件存储未接入")
		return
	}
	c, err := s.opts.Cases.GetCase(r.Context(), r.PathValue("id"))
	if err != nil || c == nil {
		writeError(rw, http.StatusNotFound, "案件不存在")
		return
	}
	report, _ := s.opts.Cases.GetCaseReport(r.Context(), c.ID)
	if report == nil {
		report = s.draftReport(r.Context(), c)
	}
	writeJSON(rw, http.StatusOK, map[string]any{"case": c, "report": report})
}

func (s *Server) handleCaseReportPut(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeError(rw, http.StatusServiceUnavailable, "案件存储未接入")
		return
	}
	c, err := s.opts.Cases.GetCase(r.Context(), r.PathValue("id"))
	if err != nil || c == nil {
		writeError(rw, http.StatusNotFound, "案件不存在")
		return
	}
	var body store.CaseReport
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	body.CaseID = c.ID
	if body.Status != "draft" && body.Status != "final" {
		body.Status = "draft"
	}
	if body.Problem == "" {
		writeError(rw, http.StatusBadRequest, "问题描述不能为空")
		return
	}
	if err := s.opts.Cases.SaveCaseReport(r.Context(), body, time.Now()); err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.opts.Cases.AddCaseEvent(r.Context(), c.ID, "report", "admin", "保存处置报告（"+body.Status+"）", time.Now())
	if body.Status == "final" {
		_ = s.opts.Cases.SetCaseStatus(r.Context(), c.ID, "resolved", time.Now())
		_ = s.opts.Cases.AddCaseEvent(r.Context(), c.ID, "resolved", "admin", "报告定稿，案件关闭", time.Now())
	}
	s.audit(r, "case.report", c.ID, body.Status)
	writeJSON(rw, http.StatusOK, body)
}

func (s *Server) handleCaseReportSend(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil || s.opts.SendReport == nil {
		writeError(rw, http.StatusServiceUnavailable, "报告发送未启用")
		return
	}
	c, err := s.opts.Cases.GetCase(r.Context(), r.PathValue("id"))
	if err != nil || c == nil {
		writeError(rw, http.StatusNotFound, "案件不存在")
		return
	}
	report, _ := s.opts.Cases.GetCaseReport(r.Context(), c.ID)
	if report == nil {
		report = s.draftReport(r.Context(), c)
	}
	text := s.renderReport(c, report)
	if err := s.opts.SendReport(r.Context(), c.ID, text); err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.opts.Cases.AddCaseEvent(r.Context(), c.ID, "report", "admin", "处置报告已发送到群", time.Now())
	s.audit(r, "case.report.send", c.ID, "")
	writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) renderReport(c *store.Case, report *store.CaseReport) string {
	return fmt.Sprintf("# %s\n\n案件：%s\n影响：%s\n\n## 问题\n%s\n\n## 证据\n%s\n\n## 根因\n%s\n\n## 动作\n%s\n\n## 验证\n%s\n\n## 后续改进\n%s\n",
		c.Title, c.ID, report.Impact, report.Problem, report.Evidence, report.RootCause, report.Actions, report.Verification, report.Prevention)
}

func (s *Server) handleReports(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeJSON(rw, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
		return
	}
	reports, err := s.opts.Cases.ListCaseReports(r.Context(), 100)
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	from, to := queryTime(r, "from"), queryTime(r, "to")
	items := make([]map[string]any, 0, len(reports))
	for _, report := range reports {
		if from != nil && report.UpdatedAt.Before(*from) {
			continue
		}
		if to != nil && report.UpdatedAt.After(*to) {
			continue
		}
		c, _ := s.opts.Cases.GetCase(r.Context(), report.CaseID)
		items = append(items, map[string]any{"report": report, "case": c})
	}
	writeJSON(rw, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) draftReport(ctx context.Context, c *store.Case) *store.CaseReport {
	report := &store.CaseReport{CaseID: c.ID, Problem: c.Title, Impact: fmt.Sprintf("%s / %s / %s", c.Severity, c.Service, c.Environment), Status: "draft"}
	if c.Summary != "" {
		report.Problem = c.Title + "\n\n" + c.Summary
	}
	if c.Source == "alertmanager" && s.opts.Incidents != nil {
		if inc, _ := s.opts.Incidents.GetIncident(ctx, c.SourceID); inc != nil {
			report.Impact = fmt.Sprintf("告警 %d 条；级别 %s；服务 %s；环境 %s", inc.AlertCount, inc.Severity, inc.Service, inc.Environment)
		}
	}
	if s.opts.Journal != nil {
		conv := ""
		if c.Source == "alertmanager" {
			conv = "aiops:" + c.SourceID
		} else if c.Type == "ticket" {
			conv = "support:" + c.SourceID
		}
		if conv != "" {
			if run, _ := s.opts.Journal.LatestRunForConversation(ctx, conv); run != nil {
				report.Evidence = run.Output
			}
		}
	}
	if approvals, _ := s.opts.Cases.ListApprovals(ctx, c.ID); len(approvals) > 0 {
		var b strings.Builder
		for _, approval := range approvals {
			fmt.Fprintf(&b, "- %s：%s（%s）\n", approval.Action, approval.Status, approval.Result)
		}
		report.Actions = b.String()
	}
	return report
}

func (s *Server) handleCaseReview(rw http.ResponseWriter, r *http.Request) {
	if s.opts.Cases == nil {
		writeError(rw, http.StatusServiceUnavailable, "案件存储未接入")
		return
	}
	var body struct {
		Reviewer   string `json:"reviewer"`
		Evidence   int    `json:"evidence"`
		RootCause  int    `json:"root_cause"`
		Safety     int    `json:"safety"`
		Usefulness int    `json:"usefulness"`
		Honesty    int    `json:"honesty"`
		Note       string `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		writeError(rw, http.StatusBadRequest, err.Error())
		return
	}
	for _, score := range []int{body.Evidence, body.RootCause, body.Safety, body.Usefulness, body.Honesty} {
		if score < 0 || score > 3 {
			writeError(rw, http.StatusBadRequest, "评分必须在 0-3")
			return
		}
	}
	if body.Reviewer == "" {
		body.Reviewer = "admin"
	}
	id := r.PathValue("id")
	review := store.CaseReview{CaseID: id, Reviewer: body.Reviewer, Evidence: body.Evidence, RootCause: body.RootCause, Safety: body.Safety, Usefulness: body.Usefulness, Honesty: body.Honesty, Note: body.Note}
	if err := s.opts.Cases.AddReview(r.Context(), review, time.Now()); err != nil {
		writeError(rw, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.opts.Cases.AddCaseEvent(r.Context(), id, "review", body.Reviewer, "提交人工评审", time.Now())
	s.audit(r, "case.review", id, body.Reviewer)
	writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
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
