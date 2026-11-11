package admin_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moying/agenthub/internal/admin"
	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/core"
)

func newServer(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{Storage: config.StorageConfig{Dir: dir}}
	store, err := admin.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := core.New(nil, nil, logger)
	return admin.New(cfg, engine, store, admin.Options{
		Version:      "test",
		FeishuStatus: func() string { return "connected" },
		Agent:        agent.Echo{},
	}, logger).Handler()
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminTestRun(t *testing.T) {
	rec := do(t, newServer(t), http.MethodPost, "/admin/api/v1/test", `{"text":"你好"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "你好") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestAdminTestRunRejectsEmpty(t *testing.T) {
	rec := do(t, newServer(t), http.MethodPost, "/admin/api/v1/test", `{"text":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestOverview(t *testing.T) {
	rec := do(t, newServer(t), http.MethodGet, "/admin/api/v1/overview", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "\"feishu\":\"connected\"") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestSkillLifecycle(t *testing.T) {
	h := newServer(t)

	rec := do(t, h, http.MethodPut, "/admin/api/v1/skills/incident", `{"content":"# 故障分析\n> 按证据排序"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = do(t, h, http.MethodGet, "/admin/api/v1/skills", "")
	if !strings.Contains(rec.Body.String(), "incident") {
		t.Fatalf("list = %s", rec.Body.String())
	}

	rec = do(t, h, http.MethodGet, "/admin/api/v1/skills/incident", "")
	if !strings.Contains(rec.Body.String(), "按证据排序") {
		t.Fatalf("get = %s", rec.Body.String())
	}

	rec = do(t, h, http.MethodDelete, "/admin/api/v1/skills/incident", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/admin/api/v1/skills/incident", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("after delete status = %d", rec.Code)
	}
}

func TestSkillRejectsBadName(t *testing.T) {
	rec := do(t, newServer(t), http.MethodPut, "/admin/api/v1/skills/..%2Fescape", `{"content":"x"}`)
	if rec.Code == http.StatusOK {
		t.Fatal("非法技能名应被拒绝")
	}
}

func TestMCPLifecycle(t *testing.T) {
	h := newServer(t)

	rec := do(t, h, http.MethodPut, "/admin/api/v1/mcps/fs", `{"transport":"stdio","command":"npx","args":["-y","server-fs"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = do(t, h, http.MethodGet, "/admin/api/v1/mcps", "")
	var body struct {
		Items []admin.MCP `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Name != "fs" || body.Items[0].Command != "npx" {
		t.Fatalf("items = %+v", body.Items)
	}

	rec = do(t, h, http.MethodDelete, "/admin/api/v1/mcps/fs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
}

func TestMCPValidation(t *testing.T) {
	rec := do(t, newServer(t), http.MethodPut, "/admin/api/v1/mcps/bad", `{"transport":"stdio"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("stdio 缺 command 应 400，实际 %d", rec.Code)
	}
}
