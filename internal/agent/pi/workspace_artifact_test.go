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
	oldFile := filepath.Join(oldDir, "old.md")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 显式把 mtime 钉在 started 之后。
	//
	// @remarks 不能依赖 os.WriteFile 自带的时间戳：overlayfs 等文件系统上
	// mtime 可能被截断到秒级，于是 info.ModTime().Before(started) 成立，
	// 回收逻辑会正确地把它当旧文件跳过——测试却报出 recovered = []。
	// 这曾让 CI 随机失败，也让真正的回归失去信号。
	stamp := started.Add(time.Second)
	if err := os.Chtimes(oldFile, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := prepareRunOutbox(ws, "run_new"); err != nil {
		t.Fatal(err)
	}
	arts := collectRunArtifacts(ws, "run_new", started)
	if len(arts) != 1 || arts[0].Name != "old.md" {
		t.Fatalf("recovered = %+v", arts)
	}
}

// TestCollectSkipsOutboxFilesOlderThanRun 钉住回收的时间边界。
//
// @remarks 回收是给「agent 复用了历史 submit 绝对路径」兜底的，只能收本次运行
// 期间写入的文件。上一轮留下的文件若被带进来，就会把上一次的产物重复交付。
func TestCollectSkipsOutboxFilesOlderThanRun(t *testing.T) {
	ws := t.TempDir()
	if err := prepareRunOutbox(ws, "run_old"); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(ws, "outbox", "run_old", "stale.md")
	if err := os.WriteFile(oldFile, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldFile, old, old); err != nil {
		t.Fatal(err)
	}
	if err := prepareRunOutbox(ws, "run_new"); err != nil {
		t.Fatal(err)
	}
	arts := collectRunArtifacts(ws, "run_new", time.Now())
	if len(arts) != 0 {
		t.Fatalf("早于本次运行的文件不应被回收: %+v", arts)
	}
}

func TestAgentsMDPointsToSubmit(t *testing.T) {
	text := buildAgentsMD("conv", 1, nil, nil)
	if !strings.Contains(text, "submit/") || !strings.Contains(text, "不要使用历史运行") {
		t.Fatalf("prompt = %s", text)
	}
}
