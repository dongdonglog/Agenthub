package admin

import "net/http"

// route 是一条管理面 API 路由。
//
// @remarks 注册表用数据而不是连续的 mux.HandleFunc 调用，目的是让
// docs/openapi.yaml 能通过测试与代码对齐：routes_test.go 会按这张表
// 校验规范里的每条 path+method 都存在，反之亦然。新增路由只改这里。
type route struct {
	// pattern 是 Go 1.7+ ServeMux 模式的 "METHOD /path" 形式。
	pattern string
	handler func(*Server, http.ResponseWriter, *http.Request)
}

// apiRoutes 是管理面全部 JSON API，顺序与文档一致。
var apiRoutes = []route{
	{"GET /admin/api/v1/overview", (*Server).handleOverview},

	{"GET /admin/api/v1/sessions", (*Server).handleSessions},
	{"GET /admin/api/v1/sessions/{id}", (*Server).handleSession},
	{"PUT /admin/api/v1/sessions/{id}/profile", (*Server).handleSessionProfile},
	{"POST /admin/api/v1/sessions/{id}/apply-config", (*Server).handleSessionApplyConfig},
	{"POST /admin/api/v1/sessions/apply-config", (*Server).handleSessionsApplyConfig},
	{"POST /admin/api/v1/sessions/{id}/reset", (*Server).handleSessionReset},
	{"POST /admin/api/v1/sessions/reset", (*Server).handleSessionsReset},
	{"PUT /admin/api/v1/sessions/{id}/priority", (*Server).handleSessionPriority},
	{"POST /admin/api/v1/deliveries/{key}/retry", (*Server).handleDeliveryRetry},

	{"GET /admin/api/v1/employees", (*Server).handleEmployees},
	{"PUT /admin/api/v1/employees/{id}", (*Server).handleEmployeeUpdate},
	{"GET /admin/api/v1/employees/{id}/stats", (*Server).handleEmployeeStats},

	{"GET /admin/api/v1/cases", (*Server).handleCases},
	{"POST /admin/api/v1/cases", (*Server).handleCaseCreate},
	{"GET /admin/api/v1/cases/{id}", (*Server).handleCase},
	{"POST /admin/api/v1/cases/{id}/handoff", (*Server).handleCaseHandoff},
	{"POST /admin/api/v1/cases/{id}/resolve", (*Server).handleCaseResolve},
	{"POST /admin/api/v1/cases/{id}/postmortem", (*Server).handleCasePostmortem},
	{"POST /admin/api/v1/cases/{id}/feedback", (*Server).handleCaseFeedback},
	{"POST /admin/api/v1/cases/{id}/review", (*Server).handleCaseReview},
	{"GET /admin/api/v1/cases/{id}/report", (*Server).handleCaseReportGet},
	{"PUT /admin/api/v1/cases/{id}/report", (*Server).handleCaseReportPut},
	{"POST /admin/api/v1/cases/{id}/report/send", (*Server).handleCaseReportSend},
	{"GET /admin/api/v1/reports", (*Server).handleReports},

	{"POST /admin/api/v1/cases/{id}/approvals", (*Server).handleCaseApprovalCreate},
	{"POST /admin/api/v1/approvals/{id}/approve", (*Server).handleApprovalDecide},
	{"POST /admin/api/v1/approvals/{id}/reject", (*Server).handleApprovalDecide},

	{"GET /admin/api/v1/incidents", (*Server).handleIncidents},
	{"GET /admin/api/v1/incidents/{id}", (*Server).handleIncident},
	{"POST /admin/api/v1/incidents/{id}/ack", (*Server).handleIncidentAck},
	{"POST /admin/api/v1/incidents/{id}/resolve", (*Server).handleIncidentResolve},

	{"GET /admin/api/v1/queue", (*Server).handleQueue},
	{"GET /admin/api/v1/runs", (*Server).handleRuns},
	{"GET /admin/api/v1/runs/{id}", (*Server).handleRunDetail},
	{"GET /admin/api/v1/artifacts/{id}", (*Server).handleArtifact},

	{"GET /admin/api/v1/tasks", (*Server).handleTasks},
	{"POST /admin/api/v1/tasks", (*Server).handleTasks},
	{"GET /admin/api/v1/tasks/{id}", (*Server).handleTask},
	{"PUT /admin/api/v1/tasks/{id}", (*Server).handleTask},
	{"DELETE /admin/api/v1/tasks/{id}", (*Server).handleTask},
	{"POST /admin/api/v1/tasks/{id}/run", (*Server).handleTaskRun},

	{"GET /admin/api/v1/skills", (*Server).handleSkills},
	{"GET /admin/api/v1/skills/{name}", (*Server).handleSkill},
	{"PUT /admin/api/v1/skills/{name}", (*Server).handleSkill},
	{"DELETE /admin/api/v1/skills/{name}", (*Server).handleSkill},
	{"PUT /admin/api/v1/skills/{name}/status", (*Server).handleSkillStatus},
	{"POST /admin/api/v1/skills/import", (*Server).handleSkillImport},
	{"GET /admin/api/v1/skills/{name}/files", (*Server).handleSkillFiles},
	{"GET /admin/api/v1/skills/{name}/files/{path...}", (*Server).handleSkillFile},
	{"PUT /admin/api/v1/skills/{name}/files/{path...}", (*Server).handleSkillFile},
	{"DELETE /admin/api/v1/skills/{name}/files/{path...}", (*Server).handleSkillFile},

	{"GET /admin/api/v1/mcps", (*Server).handleMCPs},
	{"PUT /admin/api/v1/mcps/{name}", (*Server).handleMCP},
	{"DELETE /admin/api/v1/mcps/{name}", (*Server).handleMCP},

	{"GET /admin/api/v1/settings", (*Server).handleSettings},
	{"GET /admin/api/v1/model", (*Server).handleModel},
	{"PUT /admin/api/v1/model", (*Server).handleModel},
	{"POST /admin/api/v1/model/test", (*Server).handleModelTest},
	{"GET /admin/api/v1/profiles", (*Server).handleProfiles},
	{"PUT /admin/api/v1/profiles/{name}", (*Server).handleProfile},
	{"DELETE /admin/api/v1/profiles/{name}", (*Server).handleProfile},
	{"PUT /admin/api/v1/profiles/{name}/apply", (*Server).handleProfileApply},

	{"GET /admin/api/v1/history", (*Server).handleHistory},
	{"GET /admin/api/v1/history/{conversation}/{generation}", (*Server).handleHistoryDetail},
	{"GET /admin/api/v1/audits", (*Server).handleAudits},

	{"POST /admin/api/v1/test", (*Server).handleTest},
}

// registerAPI 把所有 API 路由挂到 mux 上。
func (s *Server) registerAPI(mux *http.ServeMux) {
	for _, r := range apiRoutes {
		mux.HandleFunc(r.pattern, func(rw http.ResponseWriter, req *http.Request) {
			r.handler(s, rw, req)
		})
	}
}
