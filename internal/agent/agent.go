// Package agent 定义执行器的输入输出契约。
package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/channel"
)

// Message 是交给执行器的一条输入消息。
type Message struct {
	ID          string
	Role        string
	Text        string
	Attachments []channel.Attachment
}

// Input 是一次运行的输入。
type Input struct {
	ConversationID string
	// History 是本批之前的对话上下文。
	History  []Message
	Messages []Message
	// Progress 是阶段回调，可为空；实现方不得阻塞。
	Progress func(phase string)
	// OnTool 在每次工具调用结束后回调，可为空。
	OnTool func(name string, duration time.Duration, err error)
}

// Output 是一次运行的产出。
type Output struct {
	Text string
}

// Agent 执行一批输入并返回结果。
type Agent interface {
	Run(ctx context.Context, in Input) (Output, error)
}

// Echo 是用于本地开发与测试的确定性执行器。
//
// @remarks 它把输入按顺序回显，便于验证批次、串行与投递语义；
// 不调用任何模型。
type Echo struct{}

// Run 实现 {@link Agent}。
func (Echo) Run(_ context.Context, in Input) (Output, error) {
	parts := make([]string, 0, len(in.Messages))
	for _, m := range in.Messages {
		text := m.Text
		if text == "" {
			text = "（附件消息）"
		}
		parts = append(parts, text)
	}
	return Output{Text: fmt.Sprintf("收到 %d 条消息：%s", len(in.Messages), strings.Join(parts, " / "))}, nil
}
