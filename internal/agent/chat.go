// Package agent 定义执行器契约，并提供基于 OpenAI 兼容接口的实现。
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Tool 是可被模型调用的工具。
type Tool interface {
	// Name 是工具名，必须匹配 ^[a-zA-Z0-9_-]+$。
	Name() string
	// Description 告诉模型何时使用。
	Description() string
	// Schema 是 JSON Schema 形式的参数定义。
	Schema() map[string]any
	// Call 执行工具并返回文本结果。
	Call(ctx context.Context, args json.RawMessage) (string, error)
}

// Chat 是 OpenAI 兼容的对话执行器（OpenAI / Ark / DeepSeek / 自建网关均可）。
type Chat struct {
	// BaseURL 形如 https://api.openai.com/v1，末尾斜杠会被去掉。
	BaseURL string
	// APIKey 为空时启动会退回 Echo，不会走到这里。
	APIKey string
	// Model 是模型标识。
	Model string
	// SystemPrompt 是基础系统提示词；技能内容会追加在其后。
	SystemPrompt string
	// SkillsDir 是技能目录；每次运行读取，改动无需重启。
	SkillsDir string
	// Tools 是可用工具集合。
	Tools []Tool
	// MaxToolRounds 限制工具调用轮数，默认 6。
	MaxToolRounds int
	// Logger 记录工具调用，可为空。
	Logger *slog.Logger
	// HTTP 客户端，默认 180 秒超时。
	HTTP *http.Client
}

// Run 调用 chat/completions，并在需要时执行工具后继续对话。
func (c *Chat) Run(ctx context.Context, in Input) (Output, error) {
	if c.APIKey == "" {
		return Output{}, errors.New("未配置模型 API Key")
	}
	messages := make([]map[string]any, 0, len(in.Messages)+1)
	messages = append(messages, map[string]any{"role": "system", "content": c.systemPrompt()})
	for _, m := range in.Messages {
		messages = append(messages, map[string]any{"role": "user", "content": renderMessage(m)})
	}

	progress := in.Progress
	if progress == nil {
		progress = func(string) {}
	}
	progress("正在分析")

	rounds := c.MaxToolRounds
	if rounds <= 0 {
		rounds = 6
	}
	var last string
	for round := 0; round < rounds; round++ {
		msg, err := c.complete(ctx, messages)
		if err != nil {
			return Output{}, err
		}
		last = msg.Content
		if len(msg.ToolCalls) == 0 {
			if msg.Content == "" {
				return Output{}, errors.New("模型未返回内容")
			}
			return Output{Text: msg.Content}, nil
		}

		messages = append(messages, map[string]any{
			"role":       "assistant",
			"content":    msg.Content,
			"tool_calls": msg.ToolCalls,
		})
		for _, call := range msg.ToolCalls {
			progress("正在调用工具：" + call.Function.Name)
			result := c.executeTool(ctx, call)
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": call.ID,
				"content":      result,
			})
		}
	}
	progress("正在整理结果")
	if last == "" {
		return Output{}, errors.New("工具调用轮数超限")
	}
	return Output{Text: last}, nil
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type assistantMessage struct {
	Content   string     `json:"content"`
	ToolCalls []toolCall `json:"tool_calls"`
}

func (c *Chat) complete(ctx context.Context, messages []map[string]any) (assistantMessage, error) {
	payload := map[string]any{
		"model":       c.Model,
		"messages":    messages,
		"temperature": 0.3,
	}
	if len(c.Tools) > 0 {
		specs := make([]map[string]any, 0, len(c.Tools))
		for _, t := range c.Tools {
			specs = append(specs, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name(),
					"description": t.Description(),
					"parameters":  t.Schema(),
				},
			})
		}
		payload["tools"] = specs
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return assistantMessage{}, err
	}

	url := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return assistantMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 180 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return assistantMessage{}, fmt.Errorf("模型请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return assistantMessage{}, fmt.Errorf("模型返回 %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var out struct {
		Choices []struct {
			Message assistantMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return assistantMessage{}, fmt.Errorf("解析模型响应失败: %w", err)
	}
	if len(out.Choices) == 0 {
		return assistantMessage{}, errors.New("模型未返回 choices")
	}
	return out.Choices[0].Message, nil
}

func (c *Chat) executeTool(ctx context.Context, call toolCall) string {
	for _, t := range c.Tools {
		if t.Name() != call.Function.Name {
			continue
		}
		args := json.RawMessage(call.Function.Arguments)
		if len(args) == 0 || !json.Valid(args) {
			args = json.RawMessage(`{}`)
		}
		start := time.Now()
		out, err := t.Call(ctx, args)
		if c.Logger != nil {
			c.Logger.Info("tool call", "tool", t.Name(), "duration_ms", time.Since(start).Milliseconds(), "error", err != nil)
		}
		if err != nil {
			return "工具执行失败：" + err.Error()
		}
		return out
	}
	return "未知工具：" + call.Function.Name
}

func (c *Chat) systemPrompt() string {
	base := c.SystemPrompt
	if base == "" {
		base = "你是团队助手 AgentHub，在飞书里回答问题。回答简洁、可执行；不确定时说明缺少什么信息。"
	}
	var b strings.Builder
	b.WriteString(base)
	if c.Model != "" {
		b.WriteString("\n\n当前底层模型：")
		b.WriteString(c.Model)
		b.WriteString("（由部署配置决定；被问到用时如实回答）。")
	}

	loaded, err := LoadSkills(c.SkillsDir)
	if err != nil || len(loaded) == 0 {
		return b.String()
	}
	b.WriteString("\n\n# 可用技能\n在回答前判断是否适用；适用时遵循其步骤与输出格式。\n")
	for _, s := range loaded {
		b.WriteString("\n## ")
		b.WriteString(s.Name)
		if s.Description != "" {
			b.WriteString("：")
			b.WriteString(s.Description)
		}
		b.WriteString("\n")
		b.WriteString(s.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func renderMessage(m Message) string {
	var b strings.Builder
	b.WriteString(m.Text)
	for _, a := range m.Attachments {
		switch {
		case a.Path == "":
			fmt.Fprintf(&b, "\n[附件：%s（%s），下载失败]", a.Name, a.Kind)
		case inlineText(a.Path):
			content, err := os.ReadFile(a.Path)
			if err != nil {
				fmt.Fprintf(&b, "\n[附件：%s（%s），本地路径 %s]", a.Name, a.Kind, a.Path)
				continue
			}
			fmt.Fprintf(&b, "\n[附件：%s（%s）内容]\n%s", a.Name, a.Kind, strings.TrimSpace(string(content)))
		default:
			fmt.Fprintf(&b, "\n[附件：%s（%s），已保存到 %s，需要时用 read_file 读取]", a.Name, a.Kind, a.Path)
		}
	}
	return b.String()
}

// inlineText 判断是否把文件内容直接放进提示词。
func inlineText(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.Size() > 64*1024 {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".log", ".txt", ".md", ".json", ".yaml", ".yml", ".csv", ".ini", ".conf", ".err", ".out":
		return true
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
