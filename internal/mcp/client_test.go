package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/mcp"
)

// TestMain 让测试二进制在同一进程内充当 MCP stub 服务端。
func TestMain(m *testing.M) {
	if os.Getenv("AGENTHUB_MCP_STUB") == "1" {
		runStub()
		return
	}
	os.Exit(m.Run())
}

func runStub() {
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		if req.ID == 0 {
			continue // 通知不需要响应
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "stub", "version": "0.0.1"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "回显输入",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"text": map[string]any{"type": "string"}},
					"required":   []string{"text"},
				},
			}}}
		case "tools/call":
			result = map[string]any{"content": []map[string]any{{
				"type": "text",
				"text": "echo:" + string(req.Params),
			}}}
		default:
			result = map[string]any{}
		}
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
}

func TestClientInitializeListAndCall(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mcp.Start(ctx, "stub", os.Args[0], []string{"-test.run=TestNothing"},
		[]string{"AGENTHUB_MCP_STUB=1"}, logger)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()

	tools := client.Tools()
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}

	out, err := client.CallTool(ctx, "echo", json.RawMessage(`{"text":"你好"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if out == "" || out[:5] != "echo:" {
		t.Fatalf("output = %q", out)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	servers, err := mcp.LoadConfig("/nonexistent/mcp.yaml")
	if err != nil {
		t.Fatalf("missing file should be ok: %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("servers = %+v", servers)
	}
}
