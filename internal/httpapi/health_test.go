package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/httpapi"
	"github.com/moying/agenthub/internal/obs"
)

func newServer(t *testing.T) http.Handler {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	logger := obs.NewLogger("error", "json", io.Discard)
	metrics := obs.NewMetrics()
	return httpapi.New(cfg, logger, metrics, nil, "test").Handler()
}

func TestHealthWithoutDatabase(t *testing.T) {
	h := newServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("status = %q", body.Status)
	}
	if body.Checks["db"] != "skipped" {
		t.Fatalf("db check = %q", body.Checks["db"])
	}
}

func TestMetricsEndpoint(t *testing.T) {
	h := newServer(t)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "agenthub_http_requests_total") {
		t.Fatalf("metrics missing counter:\n%s", rec.Body.String())
	}
}

func TestUnknownPathNotFound(t *testing.T) {
	h := newServer(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
