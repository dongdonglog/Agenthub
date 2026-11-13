package core

import (
	"context"
	"errors"
	"time"

	"github.com/moying/agenthub/internal/channel"
)

// Message 是一条已受理的消息。
type Message struct {
	ID string
	// Role 是 user 或 assistant。
	Role           string
	Generation     int
	TraceID        string
	ConversationID string
	ChatID         string
	ChatKind       string
	ExternalID     string
	NoReply        bool
	SenderOpenID   string
	Text           string
	MsgType        string
	Status         string // queued | processing | done | failed | stopped
	Attachments    []channel.Attachment
	CreatedAt      time.Time
}

// Run 是一次执行。
type Run struct {
	ID             string
	ConversationID string
	State          string // running | completed | failed | interrupted
	InputIDs       []string
	Output         string
	Error          string
	StartedAt      time.Time
	EndedAt        time.Time
	InputTokens    int
	OutputTokens   int
	TraceID        string
}

// Delivery 是一次结果投递。
type Delivery struct {
	RunID          string
	IdempotencyKey string
	ChatID         string
	ReplyTo        string
	Text           string
	State          string // pending | retrying | sent | failed
	Error          string
	ExternalID     string
	Attempts       int
	CreatedAt      time.Time
	// NextAttempt 是下一次重试时间，零值表示立即。
	NextAttempt time.Time
	TraceID     string
}

// Metrics 是核心向指标系统上报的最小接口。
type Metrics interface {
	Inc(name string, labels map[string]string)
}

// RunEvent 是一条运行事件，用于管理台回放。
type RunEvent struct {
	RunID      string    `json:"run_id"`
	Kind       string    `json:"kind"` // phase | tool
	Name       string    `json:"name,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Presentation 描述一次运行对应的可更新输出。
type Presentation struct {
	ConversationID string
	ChatID         string
	ReplyTo        string
	RunID          string
	Inputs         int
}

// Presenter 是可更新输出的展示层。
//
// @remarks Open 失败时调用方退回纯文本；Close 成功表示结果已由卡片承载。
type Presenter interface {
	Open(ctx context.Context, p Presentation) (string, error)
	Update(ctx context.Context, handle, phase, detail string) error
	Close(ctx context.Context, handle, body string, failed bool) error
}

// ErrRateLimited 表示会话消息发送过于频繁。
var ErrRateLimited = errors.New("rate limited")

// Identity 是会话的展示信息。
type Identity struct {
	ChatName string `json:"chat_name"`
	UserName string `json:"user_name"`
}

// SessionInfo 是管理台的会话条目。
type SessionInfo struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"` // p2p | group | scheduled | unknown
	ChatID      string `json:"chat_id"`
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	ChatName    string `json:"chat_name"`
	UserName    string `json:"user_name"`
	Messages    int    `json:"messages"`
	LastActive  string `json:"last_active"`
	Internal    bool   `json:"internal"`
}

// ProfileSelection 是绑定到会话代的配置档案快照。
type ProfileSelection struct {
	Name   string   `json:"name"`
	Skills []string `json:"skills"`
	MCPs   []string `json:"mcps"`
}

// Journal 是核心状态的持久化接口。
//
// @remarks 实现方负责幂等：同一会话同一外部消息 ID 只应写入一次。
type Journal interface {
	SaveMessage(ctx context.Context, m *Message) (bool, error)
	SetMessageStatus(ctx context.Context, ids []string, status string) error
	SaveGeneration(ctx context.Context, conversationID string, generation int) error
	LoadGenerations(ctx context.Context) (map[string]int, error)
	SaveRun(ctx context.Context, r *Run) error
	SaveDelivery(ctx context.Context, d *Delivery) error
	LoadRecent(ctx context.Context, limit int) ([]*Message, []*Run, error)
	LoadPendingDeliveries(ctx context.Context, limit int) ([]*Delivery, error)
	SaveIdentity(ctx context.Context, conv, chatName, userName string) error
	SavePriority(ctx context.Context, conv string, priority int) error
	LoadPriorities(ctx context.Context, limit int) (map[string]int, error)
	LoadIdentities(ctx context.Context, limit int) (map[string][2]string, error)
	SaveProfileBinding(ctx context.Context, conv string, generation int, p ProfileSelection) error
	LoadProfileBindings(ctx context.Context, limit int) (map[string]ProfileSelection, error)
	SaveRunEvent(ctx context.Context, ev RunEvent) error
	LoadRunEvents(ctx context.Context, runID string) ([]RunEvent, error)
}
