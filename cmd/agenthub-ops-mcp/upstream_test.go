package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const fakeMCP = `import sys,json
for line in sys.stdin:
    req=json.loads(line)
    method=req.get("method")
    if method=="initialize":
        print(json.dumps({"jsonrpc":"2.0","id":req["id"],"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"demo","version":"1"}}}),flush=True)
    elif method=="tools/list":
        print(json.dumps({"jsonrpc":"2.0","id":req["id"],"result":{"tools":[{"name":"echo","description":"echo","inputSchema":{"type":"object"}}]}}),flush=True)
    elif method=="tools/call":
        print(json.dumps({"jsonrpc":"2.0","id":req["id"],"result":{"content":[{"type":"text","text":"pong"}]}}),flush=True)
`

func TestUpstreamGateway(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	path := filepath.Join(t.TempDir(), "fake.py")
	if err := os.WriteFile(path, []byte(fakeMCP), 0o755); err != nil {
		t.Fatal(err)
	}
	spec, _ := json.Marshal([]map[string]any{{
		"name": "demo", "command": "python3", "args": []string{path}, "prefix": "demo", "read_only": true,
	}})
	t.Setenv("AGENTHUB_MCP_UPSTREAMS", string(spec))
	s := &server{}
	s.startUpstreams()
	if len(s.upstreams) != 1 {
		t.Fatalf("upstreams=%d", len(s.upstreams))
	}
	tools := s.listTools()
	found := false
	for _, tool := range tools {
		if tool.Name == "demo__echo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tools=%+v", tools)
	}
	raw, _ := json.Marshal(map[string]any{"name": "demo__echo", "arguments": map[string]any{}})
	got, err := s.call(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("empty result")
	}
}
