package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyProfileSelectionFiltersSkillsAndMCP(t *testing.T) {
	agentDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(agentDir, "skills", "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "skills", "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "skills", "alpha", "SKILL.md"), []byte("# alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "skills", "beta", "SKILL.md"), []byte("# beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	mcp := map[string]any{"mcpServers": map[string]any{
		"alpha": map[string]any{"command": "a"},
		"beta":  map[string]any{"command": "b"},
	}}
	raw, _ := json.Marshal(mcp)
	if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	ws := t.TempDir()
	if err := applyProfileSelection(agentDir, ws, []string{"alpha"}, []string{"alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".agenthub-agent", "skills", "alpha", "SKILL.md")); err != nil {
		t.Fatalf("alpha missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".agenthub-agent", "skills", "beta")); !os.IsNotExist(err) {
		t.Fatalf("beta should be removed, err=%v", err)
	}
	var got struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	data, err := os.ReadFile(filepath.Join(ws, ".agenthub-agent", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.MCPServers) != 1 || got.MCPServers["alpha"] == nil {
		t.Fatalf("mcp = %+v", got.MCPServers)
	}
}

func TestLinkSkillExecutables(t *testing.T) {
	ws := t.TempDir()
	skill := filepath.Join(ws, ".agenthub-agent", "skills", "alpha")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(skill, "alpha-cli")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := linkSkillExecutables(ws); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, ".agenthub-bin", "alpha-cli")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if target != "../.agenthub-agent/skills/alpha/alpha-cli" {
		t.Fatalf("target = %s", target)
	}
	if err := os.RemoveAll(skill); err != nil {
		t.Fatal(err)
	}
	if err := linkSkillExecutables(ws); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("stale link should be removed, err=%v", err)
	}
}
