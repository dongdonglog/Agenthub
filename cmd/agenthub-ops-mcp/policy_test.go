package main

import "testing"

func TestToolPolicy(t *testing.T) {
	s := &server{allow: map[string]bool{"prom_query": true}, deny: map[string]bool{}}
	if !s.toolAllowed("prom_query") || s.toolAllowed("loki_query") {
		t.Fatal("allowlist not enforced")
	}
	s = &server{allow: map[string]bool{}, deny: map[string]bool{"k8s_get": true}}
	if s.toolAllowed("k8s_get") || !s.toolAllowed("prom_query") {
		t.Fatal("denylist not enforced")
	}
}
