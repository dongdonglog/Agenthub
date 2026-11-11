package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// healthResponse 是 /healthz 的响应体。
type healthResponse struct {
	Status  string            `json:"status"`
	Version string            `json:"version"`
	Checks  map[string]string `json:"checks"`
}

// handleHealth 汇总依赖状态。
//
// @remarks 数据库未配置时标记 skipped 而不是 error，便于本地只起骨架；
// 任一检查为 error 时返回 503，供编排系统与探针使用。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{"config": "ok"}

	dbStatus := "skipped"
	if s.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.Ping(ctx); err != nil {
			dbStatus = "error"
		} else {
			dbStatus = "ok"
		}
	}
	checks["db"] = dbStatus

	status := "ok"
	code := http.StatusOK
	for _, v := range checks {
		if v == "error" {
			status = "degraded"
			code = http.StatusServiceUnavailable
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(healthResponse{
		Status:  status,
		Version: s.version,
		Checks:  checks,
	})
}
