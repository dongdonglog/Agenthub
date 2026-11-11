package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
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

func TestChatIncludesHistoryRoles(t *testing.T) {
	var captured struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	c := &agent.Chat{BaseURL: srv.URL, APIKey: "k", Model: "m"}
	_, err := c.Run(context.Background(), agent.Input{
		History: []agent.Message{
			{Role: "user", Text: "第一问"},
			{Role: "assistant", Text: "第一答"},
		},
		Messages: []agent.Message{{Role: "user", Text: "第二问"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.Messages) != 4 {
		t.Fatalf("messages = %d", len(captured.Messages))
	}
	if captured.Messages[1].Role != "user" || captured.Messages[2].Role != "assistant" || captured.Messages[3].Role != "user" {
		t.Fatalf("roles = %+v", captured.Messages)
	}
}

func TestChatRetriesOnServerError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	c := &agent.Chat{BaseURL: srv.URL, APIKey: "k", Model: "m"}
	out, err := c.Run(context.Background(), agent.Input{Messages: []agent.Message{{Text: "hi"}}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Text != "ok" || attempts != 3 {
		t.Fatalf("attempts = %d, output = %q", attempts, out.Text)
	}
}

func TestChatDoesNotRetryClientError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	c := &agent.Chat{BaseURL: srv.URL, APIKey: "k", Model: "m"}
	if _, err := c.Run(context.Background(), agent.Input{Messages: []agent.Message{{Text: "hi"}}}); err == nil {
		t.Fatal("400 应直接失败")
	}
	if attempts != 1 {
		t.Fatalf("400 不应重试，实际 %d 次", attempts)
	}
}

func TestChatFallsBackToSecondaryModel(t *testing.T) {
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		models = append(models, body.Model)
		if body.Model == "primary" {
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": "fallback-ok"}}},
		})
	}))
	defer srv.Close()

	c := &agent.Chat{BaseURL: srv.URL, APIKey: "k", Model: "primary", FallbackModel: "secondary"}
	out, err := c.Run(context.Background(), agent.Input{Messages: []agent.Message{{Text: "hi"}}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Text != "fallback-ok" {
		t.Fatalf("output = %q", out.Text)
	}
	if models[len(models)-1] != "secondary" {
		t.Fatalf("最后一次模型 = %s", models[len(models)-1])
	}
}

func TestChatSendsImagesToVisionModel(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(imgPath, []byte("fake-png"), 0o644); err != nil {
		t.Fatal(err)
	}

	var captured struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": "看到了"}}},
		})
	}))
	defer srv.Close()

	c := &agent.Chat{BaseURL: srv.URL, APIKey: "k", Model: "text-model", VisionModel: "vision-model"}
	_, err := c.Run(context.Background(), agent.Input{
		Messages: []agent.Message{{
			Text:        "图里是什么",
			Attachments: []channel.Attachment{{Kind: "image", Path: imgPath, Name: "shot.png"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.Model != "vision-model" {
		t.Fatalf("model = %s", captured.Model)
	}
	var parts []map[string]any
	if err := json.Unmarshal(captured.Messages[1].Content, &parts); err != nil {
		t.Fatalf("content 不是数组: %s", captured.Messages[1].Content)
	}
	if len(parts) != 2 || parts[1]["type"] != "image_url" {
		t.Fatalf("parts = %+v", parts)
	}
}

func TestChatWithoutVisionModelSendsText(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "shot.png")
	_ = os.WriteFile(imgPath, []byte("fake"), 0o644)

	var raw json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		raw = body.Messages[1].Content
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	c := &agent.Chat{BaseURL: srv.URL, APIKey: "k", Model: "m"}
	if _, err := c.Run(context.Background(), agent.Input{
		Messages: []agent.Message{{Text: "图里是什么", Attachments: []channel.Attachment{{Kind: "image", Path: imgPath}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || raw[0] == '[' {
		t.Fatalf("未启用视觉时应为纯文本: %s", raw)
	}
}

func TestChatAggregatesUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": "ok"}}},
			"usage":   map[string]int{"prompt_tokens": 12, "completion_tokens": 7},
		})
	}))
	defer srv.Close()

	c := &agent.Chat{BaseURL: srv.URL, APIKey: "k", Model: "m"}
	out, err := c.Run(context.Background(), agent.Input{Messages: []agent.Message{{Text: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Usage.InputTokens != 12 || out.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v", out.Usage)
	}
}

func TestToolLogCarriesTraceID(t *testing.T) {
	round := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round++
		if round == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": map[string]any{
					"content": "",
					"tool_calls": []any{map[string]any{
						"id": "c1", "type": "function",
						"function": map[string]any{"name": "mcp_demo_echo", "arguments": "{}"},
					}},
				}}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	tool := &fakeTool{name: "mcp_demo_echo"}
	c := &agent.Chat{BaseURL: srv.URL, APIKey: "k", Model: "m", Tools: []agent.Tool{tool}, Logger: logger}
	if _, err := c.Run(context.Background(), agent.Input{
		TraceID:  "tr_test",
		RunID:    "run_9",
		Messages: []agent.Message{{Text: "hi"}},
	}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "trace_id=tr_test") || !strings.Contains(out, "run_id=run_9") {
		t.Fatalf("日志缺少 trace: %s", out)
	}
	if !strings.Contains(out, "tool call") {
		t.Fatalf("日志缺少工具调用: %s", out)
	}
}
