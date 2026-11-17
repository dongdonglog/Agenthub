package admin_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moying/agenthub/internal/admin"
	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/core"
)

func TestAdminRBAC(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Storage: config.StorageConfig{Dir: dir}, Admin: config.AdminConfig{
		Token: "admin-token", OperatorToken: "op-token", ViewToken: "view-token",
	}}
	st, err := admin.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := admin.New(cfg, core.New(nil, nil, logger), st, admin.Options{}, logger).Handler()

	req := httptest.NewRequest(http.MethodGet, "/admin/api/v1/overview", nil)
	req.Header.Set("Authorization", "Bearer view-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("view GET = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/api/v1/sessions/reset", bytes.NewReader(nil))
	req.Header.Set("Authorization", "Bearer view-token")
	req.Header.Set("X-Requested-With", "agenthub")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("view POST = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/api/v1/sessions/reset", bytes.NewReader(nil))
	req.Header.Set("Authorization", "Bearer op-token")
	req.Header.Set("X-Requested-With", "agenthub")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("operator POST = %d body=%s", rec.Code, rec.Body.String())
	}
}
