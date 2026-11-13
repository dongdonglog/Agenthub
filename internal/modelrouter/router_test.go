package modelrouter

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/modelcfg"
)

type stubMetrics struct{ counts map[string]int }

func (m *stubMetrics) Inc(name string, labels map[string]string) {
	if m.counts == nil {
		m.counts = map[string]int{}
	}
	m.counts[name]++
}
func (m *stubMetrics) Add(string, float64, map[string]string) {}
func (m *stubMetrics) Set(string, float64, map[string]string) {}

func upstream(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
}

func newRouter(t *testing.T, servers []*httptest.Server, failures int) (*Router, *stubMetrics) {
	t.Helper()
	cfg := modelcfg.Config{Version: 2}
	for i, srv := range servers {
		name := string(rune('a' + i))
		cfg.Providers = append(cfg.Providers, modelcfg.Provider{Name: name, BaseURL: srv.URL})
		cfg.Models = append(cfg.Models, modelcfg.Model{ID: name, Provider: name, Model: "m-" + name, Capabilities: []string{"text"}})
	}
	ids := make([]string, 0, len(servers))
	for _, m := range cfg.Models {
		ids = append(ids, m.ID)
	}
	cfg.Routes = []modelcfg.Route{{
		Name: "main", Candidates: ids,
		Retry:   modelcfg.Retry{MaxAttempts: 1},
		Circuit: modelcfg.Circuit{Failures: failures, CooldownSeconds: 60},
	}}
	metrics := &stubMetrics{}
	router := New(cfg, "tok", metrics, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = router.Close(context.Background()) })
	return router, metrics
}

func chat(t *testing.T, router *Router, stream bool) *http.Response {
	t.Helper()
	body := `{"model":"main","messages":[{"role":"user","content":"hi"}],"stream":` + map[bool]string{true: "true", false: "false"}[stream] + `}`
	req, _ := http.NewRequest(http.MethodPost, router.URL()+"/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestRouteSuccess(t *testing.T) {
	srv := upstream(t, 200, `{"choices":[{"message":{"content":"ok"}}]}`)
	defer srv.Close()
	router, _ := newRouter(t, []*httptest.Server{srv}, 3)
	resp := chat(t, router, false)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), "ok") {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	if resp.Header.Get("X-AgentHub-Model") != "m-a" {
		t.Fatalf("model header = %s", resp.Header.Get("X-AgentHub-Model"))
	}
}

func TestFallbackOn429(t *testing.T) {
	bad := upstream(t, 429, `{"error":"rate limited"}`)
	defer bad.Close()
	good := upstream(t, 200, `{"choices":[{"message":{"content":"fallback"}}]}`)
	defer good.Close()
	router, _ := newRouter(t, []*httptest.Server{bad, good}, 3)
	resp := chat(t, router, false)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), "fallback") {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	if resp.Header.Get("X-AgentHub-Attempts") != "2" {
		t.Fatalf("attempts = %s", resp.Header.Get("X-AgentHub-Attempts"))
	}
}

func TestNoFallbackOn400(t *testing.T) {
	called := false
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"error":"bad request"}`)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = io.WriteString(w, `{}`)
	}))
	defer good.Close()
	router, _ := newRouter(t, []*httptest.Server{bad, good}, 3)
	resp := chat(t, router, false)
	defer resp.Body.Close()
	if called {
		t.Fatal("400 不应触发备选")
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestStreamFallbackBeforeFirstByte(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "data: hello\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer good.Close()
	router, _ := newRouter(t, []*httptest.Server{bad, good}, 3)
	resp := chat(t, router, true)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "data: hello") {
		t.Fatalf("stream body = %s", raw)
	}
	if resp.Header.Get("X-AgentHub-Attempts") != "2" {
		t.Fatalf("attempts = %s", resp.Header.Get("X-AgentHub-Attempts"))
	}
}

func TestCircuitOpensAndSkips(t *testing.T) {
	bad := upstream(t, 500, `{"error":"boom"}`)
	defer bad.Close()
	good := upstream(t, 200, `{"choices":[]}`)
	defer good.Close()
	router, _ := newRouter(t, []*httptest.Server{bad, good}, 1)

	for i := 0; i < 2; i++ {
		resp := chat(t, router, false)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	health := router.Health()
	open := false
	for _, item := range health {
		if item.Candidate == "a" && item.Open {
			open = true
		}
	}
	if !open {
		t.Fatalf("候选 a 应该熔断: %+v", health)
	}

	// 熔断后直接走 b，不再请求 a。
	start := time.Now()
	resp := chat(t, router, false)
	defer resp.Body.Close()
	if resp.Header.Get("X-AgentHub-Attempts") != "1" {
		t.Fatalf("熔断后 attempts = %s", resp.Header.Get("X-AgentHub-Attempts"))
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("熔断未生效")
	}
}

func TestVisionFilter(t *testing.T) {
	srv := upstream(t, 200, `{"choices":[]}`)
	defer srv.Close()
	router, _ := newRouter(t, []*httptest.Server{srv}, 3)
	body := `{"model":"main","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,x"}}]}]}`
	req, _ := http.NewRequest(http.MethodPost, router.URL()+"/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("无 vision 候选时应 400，实际 %d", resp.StatusCode)
	}
}

func TestMetricsAndHeaders(t *testing.T) {
	srv := upstream(t, 200, `{"choices":[]}`)
	defer srv.Close()
	router, metrics := newRouter(t, []*httptest.Server{srv}, 3)
	resp := chat(t, router, false)
	resp.Body.Close()
	if metrics.counts["model_router_requests_total"] == 0 {
		t.Fatal("未记录指标")
	}
	if router.URL() == "" || router.Token() != "tok" {
		t.Fatal("URL/token 未初始化")
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(`{"model":"main"}`), &payload)
}

func TestUnknownRoute(t *testing.T) {
	srv := upstream(t, 200, `{}`)
	defer srv.Close()
	router, _ := newRouter(t, []*httptest.Server{srv}, 3)
	req, _ := http.NewRequest(http.MethodPost, router.URL()+"/chat/completions", strings.NewReader(`{"model":"nope","messages":[]}`))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
