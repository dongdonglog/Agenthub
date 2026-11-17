package main

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"
)

type auditEvent struct {
	Time       string `json:"time"`
	Tool       string `json:"tool"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

func (s *server) loadPolicy() {
	s.allow = splitSet(os.Getenv("AGENTHUB_MCP_TOOL_ALLOW"))
	s.deny = splitSet(os.Getenv("AGENTHUB_MCP_TOOL_DENY"))
	if path := os.Getenv("AGENTHUB_MCP_AUDIT_FILE"); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			log.Printf("mcp gateway: audit file unavailable: %v", err)
		} else {
			s.audit = f
		}
	}
}

func splitSet(raw string) map[string]bool {
	out := map[string]bool{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out[item] = true
		}
	}
	return out
}

func (s *server) toolAllowed(name string) bool {
	if len(s.allow) > 0 && !s.allow[name] {
		return false
	}
	return !s.deny[name]
}

func (s *server) filterTools(in []tool) []tool {
	out := make([]tool, 0, len(in))
	for _, item := range in {
		if s.toolAllowed(item.Name) {
			out = append(out, item)
		}
	}
	return out
}

func (s *server) auditCall(name string, started time.Time, err error) {
	event := auditEvent{Time: started.UTC().Format(time.RFC3339Nano), Tool: name, DurationMS: time.Since(started).Milliseconds()}
	if err != nil {
		event.Error = err.Error()
	}
	raw, _ := json.Marshal(event)
	log.Printf("mcp gateway call %s", string(raw))
	if s.audit != nil {
		_, _ = s.audit.Write(append(raw, '\n'))
	}
}
