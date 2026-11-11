package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/moying/agenthub/internal/agent"
)

func TestReadFileToolWithinRoot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "app.log")
	if err := os.WriteFile(path, []byte("line-1\nline-2"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := &agent.ReadFileTool{Root: root}

	out, err := tool.Call(context.Background(), json.RawMessage(`{"path":"app.log"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != "line-1\nline-2" {
		t.Fatalf("output = %q", out)
	}

	out, err = tool.Call(context.Background(), json.RawMessage(`{"path":"`+path+`"}`))
	if err != nil || out == "" {
		t.Fatalf("绝对路径读取失败: %v", err)
	}
}

func TestReadFileToolRejectsOutsideRoot(t *testing.T) {
	tool := &agent.ReadFileTool{Root: t.TempDir()}
	if _, err := tool.Call(context.Background(), json.RawMessage(`{"path":"/etc/passwd"}`)); err == nil {
		t.Fatal("应拒绝读取附件目录之外的文件")
	}
	if _, err := tool.Call(context.Background(), json.RawMessage(`{"path":"../escape"}`)); err == nil {
		t.Fatal("应拒绝路径穿越")
	}
}

func TestReadFileToolTruncates(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := &agent.ReadFileTool{Root: root, MaxBytes: 5}
	out, err := tool.Call(context.Background(), json.RawMessage(`{"path":"big.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) <= 5 {
		t.Fatalf("应包含截断提示: %q", out)
	}
}
