// Package channel 定义与平台无关的入站消息、出站请求与通道能力。
//
// 通道实现只做协议互转，不感知队列、会话与投递语义。
package channel

import (
	"context"
	"time"
)

// Sender 是消息发送者。
type Sender struct {
	OpenID string
	Name   string
}

// Chat 是消息所在会话。
type Chat struct {
	ID   string
	Kind string // p2p | group
	Name string
}

// Attachment 是入站附件描述。
type Attachment struct {
	Kind    string // image | file | audio | media
	FileKey string
	Name    string
	Mime    string
	Size    int64
}

// Inbound 是通道转换后的标准入站消息。
type Inbound struct {
	Channel      string
	Tenant       string
	ExternalID   string
	SentAt       time.Time
	Sender       Sender
	Chat         Chat
	Text         string
	MsgType      string
	MentionsBot  bool
	ParentID     string
	Attachments  []Attachment
	RawEventType string
}

// Outbound 是一次文本发送请求。
type Outbound struct {
	ChatID  string
	ReplyTo string
	Text    string
}

// Receipt 是发送结果。
type Receipt struct {
	ExternalID string
}

// Channel 是发送侧的最小接口。
//
// @remarks 入站由连接层直接交给核心，不经过本接口。
type Channel interface {
	SendText(ctx context.Context, out Outbound) (Receipt, error)
}
