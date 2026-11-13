package pi

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
)

func buildFakePi(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "fakepi")
	cmd := exec.Command("go", "build", "-o", out, "./testdata/fakepi")
	cmd.Dir = "."
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("构建 fake pi 失败: %v\n%s", err, raw)
	}
	return out
}

func newTestRuntime(t *testing.T, mode string, timeout time.Duration) (*Runtime, string, string) {
	t.Helper()
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "SYSTEM.md"), []byte("system"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PI_MODE", mode)
	rt := New(Config{
		Bin:          buildFakePi(t),
		AgentDir:     agentDir,
		SessionDir:   filepath.Join(root, "sessions"),
		WorkspaceDir: filepath.Join(root, "workspaces"),
		Timeout:      timeout,
		AbortGrace:   200 * time.Millisecond,
		Isolation:    "process_group",
	})
	return rt, filepath.Join(root, "sessions"), filepath.Join(root, "workspaces")
}

func input(conv string) agent.Input {
	return agent.Input{
		ConversationID: conv,
		Generation:     1,
		RunID:          "run_test",
		Messages: []agent.Message{
			{Text: "第一问"},
			{Text: "第二问"},
		},
	}
}

func TestRuntimeCompleted(t *testing.T) {
	rt, sessions, workspaces := newTestRuntime(t, "completed", 5*time.Second)
	out, err := rt.Run(context.Background(), input("feishu:chat:user"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "这是 pi 的回答" {
		t.Fatalf("text = %q", out.Text)
	}

	matches, _ := filepath.Glob(filepath.Join(sessions, "*.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("session files = %v", matches)
	}
	raw, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(raw), `"version":3`) {
		t.Fatalf("缺少会话头: %s", raw)
	}
	if strings.Count(string(raw), `"role":"user"`) != 1 || !strings.Contains(string(raw), "第一问") {
		t.Fatalf("应预置 1 条 user 消息（最后一条走 prompt）: %s", raw)
	}
	if !strings.Contains(string(raw), `"customType":"agenthub.batch"`) {
		t.Fatalf("缺少批次标记")
	}

	wsMatches, _ := filepath.Glob(filepath.Join(workspaces, "*", ".agenthub-agent", "SYSTEM.md"))
	if len(wsMatches) != 1 {
		t.Fatalf("工作区未复制 agent dir: %v", wsMatches)
	}
	agentsMD, _ := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(wsMatches[0])), "AGENTS.md"))
	if !strings.Contains(string(agentsMD), "---SPLIT---") {
		t.Fatalf("AGENTS.md 缺少输出约定")
	}
}

func TestRuntimeLinksAttachments(t *testing.T) {
	rt, _, workspaces := newTestRuntime(t, "completed", 5*time.Second)
	dir := t.TempDir()
	file := filepath.Join(dir, "现场日志.txt")
	if err := os.WriteFile(file, []byte("日志内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := input("feishu:chat:attach")
	in.Messages[0].Attachments = []channel.Attachment{{Kind: "file", Name: "现场日志.txt", Path: file}}

	if _, err := rt.Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(workspaces, "*", "attachments", "*"))
	if len(matches) != 1 {
		t.Fatalf("附件未链接: %v", matches)
	}
	if raw, err := os.ReadFile(matches[0]); err != nil || string(raw) != "日志内容" {
		t.Fatalf("附件内容不一致: %s %v", raw, err)
	}
}

func TestRuntimeFailed(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "failed", 5*time.Second)
	out, err := rt.Run(context.Background(), input("feishu:chat:user"))
	if err == nil || !strings.Contains(err.Error(), "pi 运行失败") {
		t.Fatalf("err = %v", err)
	}
	if out.Text != "" {
		t.Fatalf("失败不应产出文本: %q", out.Text)
	}
}

func TestRuntimeTimeout(t *testing.T) {
	rt, _, _ := newTestRuntime(t, "hang", 300*time.Millisecond)
	start := time.Now()
	_, err := rt.Run(context.Background(), input("feishu:chat:user"))
	if err == nil {
		t.Fatal("超时应返回错误")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("中止耗时过长: %s", time.Since(start))
	}
}
