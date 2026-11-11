package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/moying/agenthub/internal/core"
)

// cardBodyLimit 是卡片正文的安全上限，超过时退回文本投递。
const cardBodyLimit = 3000

// CardPresenter 用飞书交互卡片展示运行进度与结果。
type CardPresenter struct {
	client *Client
	log    *slog.Logger
	// stopButton 为 true 时在卡片上渲染停止按钮（需配置回调地址）。
	stopButton bool

	mu    sync.Mutex
	runOf map[string]string
}

// NewCardPresenter 构造卡片展示层。
func NewCardPresenter(client *Client, log *slog.Logger) *CardPresenter {
	if log == nil {
		log = slog.Default()
	}
	return &CardPresenter{client: client, log: log, runOf: make(map[string]string)}
}

// WithStopButton 开启卡片停止按钮。
func (p *CardPresenter) WithStopButton(enabled bool) *CardPresenter {
	p.stopButton = enabled
	return p
}

// Open 创建"处理中"卡片。
func (p *CardPresenter) Open(ctx context.Context, pres core.Presentation) (string, error) {
	stopRun := ""
	if p.stopButton {
		stopRun = pres.RunID
	}
	card := buildCard("处理中", fmt.Sprintf("已收到 %d 条消息，正在处理…", pres.Inputs), "blue", stopRun)
	handle, err := p.client.CreateCard(ctx, pres.ChatID, pres.ReplyTo, card)
	if err == nil && p.stopButton {
		p.mu.Lock()
		p.runOf[handle] = pres.RunID
		p.mu.Unlock()
	}
	return handle, err
}

// Update 刷新阶段文本。
func (p *CardPresenter) Update(ctx context.Context, handle, phase, detail string) error {
	body := phase
	if detail != "" {
		body += "\n" + detail
	}
	p.mu.Lock()
	runID := p.runOf[handle]
	p.mu.Unlock()
	stopRun := ""
	if p.stopButton {
		stopRun = runID
	}
	return p.client.UpdateCard(ctx, handle, buildCard("处理中", body, "blue", stopRun))
}

// Close 写入最终结果。
//
// @remarks 结果过长时更新为提示卡并返回错误，让引擎退回文本投递，避免截断答案。
func (p *CardPresenter) Close(ctx context.Context, handle, body string, failed bool) error {
	p.mu.Lock()
	delete(p.runOf, handle)
	p.mu.Unlock()
	if len([]rune(body)) > cardBodyLimit {
		_ = p.client.UpdateCard(ctx, handle, buildCard("结果较长", "完整内容见下一条消息。", "orange", ""))
		return fmt.Errorf("结果超过卡片上限")
	}
	template, title := "green", "处理完成"
	if failed {
		template, title = "red", "处理失败"
	}
	return p.client.UpdateCard(ctx, handle, buildCard(title, body, template, ""))
}

func buildCard(title, body, template, stopRun string) string {
	elements := []any{
		map[string]any{"tag": "div", "text": map[string]string{"tag": "lark_md", "content": body}},
	}
	if stopRun != "" {
		elements = append(elements, map[string]any{
			"tag": "action",
			"actions": []any{map[string]any{
				"tag":  "button",
				"text": map[string]string{"tag": "plain_text", "content": "停止"},
				"type": "danger",
				"behaviors": []any{map[string]any{
					"type":  "callback",
					"value": map[string]string{"action": "stop", "run_id": stopRun},
				}},
			}},
		})
	}
	card := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"template": template,
			"title":    map[string]string{"tag": "plain_text", "content": title},
		},
		"elements": elements,
	}
	raw, _ := json.Marshal(card)
	return string(raw)
}
