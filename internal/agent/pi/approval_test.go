package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCollectApprovalRequests(t *testing.T) {
	dir := t.TempDir()
	if got := collectApprovalRequests(dir); len(got) != 0 {
		t.Fatalf("空目录应无申请: %v", got)
	}

	valid := `{"action":"k8s_scale","namespace":"prod","deployment":"checkout","replicas":3}`
	if err := os.WriteFile(filepath.Join(dir, "approval-1.json"), []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	// 非 JSON、非 .json 与子目录都应被忽略。
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := collectApprovalRequests(dir)
	if len(got) != 1 {
		t.Fatalf("approvals = %s", got)
	}
	var parsed map[string]any
	if err := json.Unmarshal(got[0], &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["action"] != "k8s_scale" {
		t.Fatalf("parsed = %v", parsed)
	}
}
