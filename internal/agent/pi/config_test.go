package pi

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSyncAgentConfig(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	if err := PrepareAgentDir(AgentConfig{Dir: agentDir, BaseURL: "https://gw/v1", APIKey: "k", Model: "new-model"}); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(root, "workspaces", "abc-g1")
	agentCopy := filepath.Join(ws, ".agenthub-agent")
	if err := os.MkdirAll(agentCopy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentCopy, "models.json"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SyncAgentConfig(filepath.Join(root, "workspaces"), agentDir); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(agentCopy, "models.json"))
	if string(raw) == "old" || len(raw) < 10 {
		t.Fatalf("models.json 未同步: %s", raw)
	}
}

func TestCleanupExpired(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "workspaces", "old-g1")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(sessions, "old-g1.jsonl")
	if err := os.WriteFile(old, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().AddDate(0, 0, -30)
	if err := os.Chtimes(ws, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}

	removed, err := CleanupExpired(filepath.Join(root, "workspaces"), sessions, time.Now().AddDate(0, 0, -7))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d", removed)
	}
	if _, err := os.Stat(ws); err == nil {
		t.Fatal("工作区未删除")
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("会话文件未删除")
	}
}
