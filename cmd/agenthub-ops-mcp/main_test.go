package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPrometheusToolReadOnly(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()
	s := &server{promURL: srv.URL, http: srv.Client()}
	out, err := s.runTool("prom_query", map[string]any{"query": "up"})
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "up" || !strings.Contains(out, "success") {
		t.Fatalf("query=%q out=%s", gotQuery, out)
	}
}

func TestLokiToolRequiresConfig(t *testing.T) {
	s := &server{http: http.DefaultClient}
	if _, err := s.runTool("loki_query", map[string]any{"query": "{app=\"api\"}"}); err == nil {
		t.Fatal("expected missing config error")
	}
}

func TestToolsOnlyConfigured(t *testing.T) {
	s := &server{promURL: "http://prom", http: http.DefaultClient}
	s.tools = s.listTools()
	names := map[string]bool{}
	for _, tool := range s.tools {
		names[tool.Name] = true
	}
	if !names["prom_query"] || names["loki_query"] {
		t.Fatalf("tools = %+v", names)
	}
}

func TestTempoTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/search":
			if r.URL.Query().Get("tags") != "service.name=api" {
				t.Errorf("tags = %s", r.URL.Query().Get("tags"))
			}
			_, _ = w.Write([]byte(`{"traces":[]}`))
		case "/api/traces/abc":
			_, _ = w.Write([]byte(`{"traceID":"abc"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := &server{tempoURL: srv.URL, http: srv.Client()}
	out, err := s.runTool("tempo_search", map[string]any{"tags": "service.name=api"})
	if err != nil || !strings.Contains(out, "traces") {
		t.Fatalf("out=%s err=%v", out, err)
	}
	out, err = s.runTool("tempo_trace", map[string]any{"trace_id": "abc"})
	if err != nil || !strings.Contains(out, "abc") {
		t.Fatalf("out=%s err=%v", out, err)
	}
}

func TestKubectlAllowlist(t *testing.T) {
	dir := t.TempDir()
	bin := dir + "/kubectl"
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &server{kubectl: bin, http: http.DefaultClient}
	out, err := s.runTool("k8s_get", map[string]any{"resource": "pods", "namespace": "prod"})
	if err != nil || !strings.Contains(out, "pods") || !strings.Contains(out, "-n prod") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if _, err := s.runTool("k8s_get", map[string]any{"resource": "secrets"}); err == nil {
		t.Fatal("secrets should be rejected")
	}
	if _, err := s.runTool("k8s_logs", map[string]any{"pod": "../../etc"}); err == nil {
		t.Fatal("invalid pod should be rejected")
	}
}
