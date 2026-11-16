package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareRunOutboxLinksSubmit(t *testing.T) {
	ws := t.TempDir()
	if err := prepareRunOutbox(ws, "run_1"); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(ws, "submit"))
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join("outbox", "run_1") {
		t.Fatalf("submit -> %s", target)
	}
	if err := os.WriteFile(filepath.Join(ws, "submit", "报告.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	arts := collectRunArtifacts(ws, "run_1", time.Now().Add(-time.Minute))
	if len(arts) != 1 || arts[0].Name != "报告.md" || arts[0].Kind != "file" {
		t.Fatalf("artifacts = %+v", arts)
	}
}

func TestCollectRejectsSymlinkAndDirectory(t *testing.T) {
	ws := t.TempDir()
	if err := prepareRunOutbox(ws, "run_2"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(ws, "outbox", "run_2")
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, ".hidden"), filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.md"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	arts := collectRunArtifacts(ws, "run_2", time.Now().Add(-time.Minute))
	if len(arts) != 1 || arts[0].Name != "ok.md" {
		t.Fatalf("artifacts = %+v", arts)
	}
}

func TestCollectRecoversStaleOutbox(t *testing.T) {
	ws := t.TempDir()
	started := time.Now()
	if err := prepareRunOutbox(ws, "run_old"); err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(ws, "outbox", "run_old")
	if err := os.WriteFile(filepath.Join(oldDir, "old.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := prepareRunOutbox(ws, "run_new"); err != nil {
		t.Fatal(err)
	}
	arts := collectRunArtifacts(ws, "run_new", started)
	if len(arts) != 1 || arts[0].Name != "old.md" {
		t.Fatalf("recovered = %+v", arts)
	}
}

func TestAgentsMDPointsToSubmit(t *testing.T) {
	text := buildAgentsMD("conv", 1, nil)
	if !strings.Contains(text, "submit/") || !strings.Contains(text, "不要使用历史运行") {
		t.Fatalf("prompt = %s", text)
	}
}
