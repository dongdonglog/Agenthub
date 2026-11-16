package main

import (
	"net/http"
	"net/http/httptest"
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
