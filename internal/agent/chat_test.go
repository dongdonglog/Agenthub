package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moying/agenthub/internal/agent"
)

func TestChatSendsSystemPromptSkillsAndMessages(t *testing.T) {
	var captured struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	var auth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": "模型回答"}}},
		})
	}))
	defer srv.Close()

	dir := t.TempDir()
	skill := "# 故障分析\n> 按证据排序\n\n1. 看日志\n2. 给结论\n"
	if err := os.WriteFile(filepath.Join(dir, "incident.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &agent.Chat{
		BaseURL:      srv.URL,
		APIKey:       "test-key",
		Model:        "test-model",
		SystemPrompt: "基础提示",
		SkillsDir:    dir,
	}
	out, err := c.Run(context.Background(), agent.Input{
		Messages: []agent.Message{{ID: "m1", Text: "机械臂停了"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Text != "模型回答" {
		t.Fatalf("output = %q", out.Text)
	}
	if auth != "Bearer test-key" {
		t.Fatalf("auth = %q", auth)
	}
	if captured.Model != "test-model" {
		t.Fatalf("model = %q", captured.Model)
	}
	if len(captured.Messages) != 2 || captured.Messages[0].Role != "system" {
		t.Fatalf("messages = %+v", captured.Messages)
	}
	if !strings.Contains(captured.Messages[0].Content, "基础提示") || !strings.Contains(captured.Messages[0].Content, "故障分析") {
		t.Fatalf("system prompt 未包含技能: %s", captured.Messages[0].Content)
	}
	if captured.Messages[1].Role != "user" || captured.Messages[1].Content != "机械臂停了" {
		t.Fatalf("user message = %+v", captured.Messages[1])
	}
}

func TestChatReturnsErrorOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	c := &agent.Chat{BaseURL: srv.URL, APIKey: "k", Model: "m"}
	if _, err := c.Run(context.Background(), agent.Input{}); err == nil {
		t.Fatal("非 200 应返回错误")
	}
}

func TestChatRequiresAPIKey(t *testing.T) {
	c := &agent.Chat{}
	if _, err := c.Run(context.Background(), agent.Input{}); err == nil {
		t.Fatal("缺少 API Key 应报错")
	}
}

type fakeTool struct {
	name  string
	calls int
}

func (f *fakeTool) Name() string        { return f.name }
func (f *fakeTool) Description() string { return "测试工具" }
func (f *fakeTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (f *fakeTool) Call(context.Context, json.RawMessage) (string, error) {
	f.calls++
	return "工具结果 42", nil
}

func TestChatExecutesToolCallsAndContinues(t *testing.T) {
	round := 0
	var secondBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		round++
		if round == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{
					"content": "",
					"tool_calls": []any{map[string]any{
						"id": "call_1", "type": "function",
						"function": map[string]any{"name": "mcp_demo_echo", "arguments": `{"text":"hi"}`},
					}},
				}}},
			})
			return
		}
		secondBody = string(body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": "最终回答"}}},
		})
	}))
	defer srv.Close()

	tool := &fakeTool{name: "mcp_demo_echo"}
	c := &agent.Chat{BaseURL: srv.URL, APIKey: "k", Model: "m", Tools: []agent.Tool{tool}}
	out, err := c.Run(context.Background(), agent.Input{Messages: []agent.Message{{Text: "用工具查一下"}}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Text != "最终回答" {
		t.Fatalf("output = %q", out.Text)
	}
	if tool.calls != 1 {
		t.Fatalf("tool calls = %d", tool.calls)
	}
	if !strings.Contains(secondBody, "工具结果 42") || !strings.Contains(secondBody, `"role":"tool"`) {
		t.Fatalf("第二轮请求未带工具结果: %s", secondBody)
	}
}
