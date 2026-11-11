// Package agent 定义执行器契约，并提供基于 OpenAI 兼容接口的实现。
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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
	// HTTP 客户端，默认 120 秒超时。
	HTTP *http.Client
}

// Run 调用 chat/completions 并返回首条回复。
func (c *Chat) Run(ctx context.Context, in Input) (Output, error) {
	if c.APIKey == "" {
		return Output{}, errors.New("未配置模型 API Key")
	}
	messages := make([]chatMessage, 0, len(in.Messages)+1)
	messages = append(messages, chatMessage{Role: "system", Content: c.systemPrompt()})
	for _, m := range in.Messages {
		messages = append(messages, chatMessage{Role: "user", Content: renderMessage(m)})
	}

	payload := map[string]any{
		"model":       c.Model,
		"messages":    messages,
		"temperature": 0.3,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Output{}, err
	}

	url := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return Output{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Output{}, fmt.Errorf("模型请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return Output{}, fmt.Errorf("模型返回 %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return Output{}, fmt.Errorf("解析模型响应失败: %w", err)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return Output{}, errors.New("模型未返回内容")
	}
	return Output{Text: out.Choices[0].Message.Content}, nil
}

func (c *Chat) systemPrompt() string {
	base := c.SystemPrompt
	if base == "" {
		base = "你是团队助手 AgentHub，在飞书里回答问题。回答简洁、可执行；不确定时说明缺少什么信息。"
	}
	loaded, err := LoadSkills(c.SkillsDir)
	if err != nil || len(loaded) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
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
		fmt.Fprintf(&b, "\n[附件：%s（%s）]", a.Name, a.Kind)
	}
	return b.String()
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
