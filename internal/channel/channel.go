// Package channel 定义与平台无关的入站消息、出站请求与通道能力。
//
// 通道实现只做协议互转，不感知队列、会话与投递语义。
package channel

import (
	"context"
	"errors"
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
	// Path 是通道落盘后的本地路径；为空表示未下载成功。
	Path string
}

// Inbound 是通道转换后的标准入站消息。
type Inbound struct {
	Channel     string
	Tenant      string
	ExternalID  string
	SentAt      time.Time
	Sender      Sender
	Chat        Chat
	Text        string
	MsgType     string
	MentionsBot bool
	ParentID    string
	// NoReply 为 true 时投递不回挂消息（用于定时任务等没有平台消息 ID 的来源）。
	NoReply      bool
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

// Artifact 是运行产物在本地落盘后的描述。
type Artifact struct {
	// Name 是交付给用户的文件名，不含路径。
	Name string
	// Path 是本地可读路径；发送成功后由核心按保留策略清理。
	Path string
	// Kind 为 file 或 image。
	Kind string
	Size int64
}

// OutboundArtifact 是一次产物发送请求。
type OutboundArtifact struct {
	ChatID   string
	ReplyTo  string
	Artifact Artifact
}

// ArtifactSender 是通道的可选文件能力。
//
// @remarks 不支持文件的通道不要实现该接口，核心会明确提示而不是静默丢弃。
type ArtifactSender interface {
	SendArtifact(ctx context.Context, out OutboundArtifact) (Receipt, error)
}

// ErrUnsupported 表示当前通道不支持该发送类型。
var ErrUnsupported = errors.New("通道不支持该发送类型")

// FailureKind 描述通道失败后是否可以安全重试。
type FailureKind int

const (
	// FailureDefinite 表示通道明确拒绝或本地无法发送，重试没有意义。
	FailureDefinite FailureKind = iota
	// FailureRetryable 表示通道明确未接受（如限流），可以安全重试。
	FailureRetryable
	// FailureAmbiguous 表示请求可能已被接受，但结果未知；禁止自动重试。
	FailureAmbiguous
)

// DeliveryError 给发送错误附加可重试性。
type DeliveryError struct {
	Kind FailureKind
	Err  error
}

func (e *DeliveryError) Error() string { return e.Err.Error() }
func (e *DeliveryError) Unwrap() error { return e.Err }

// Retryable 标记明确未接受、可安全重试的失败。
func Retryable(err error) error { return &DeliveryError{Kind: FailureRetryable, Err: err} }

// Ambiguous 标记结果不确定、不可自动重试的失败。
func Ambiguous(err error) error { return &DeliveryError{Kind: FailureAmbiguous, Err: err} }

// Definite 标记明确失败、不可重试的错误。
func Definite(err error) error { return &DeliveryError{Kind: FailureDefinite, Err: err} }

// FailureKindOf 返回错误的重试分类；未知错误按明确失败处理。
func FailureKindOf(err error) FailureKind {
	var de *DeliveryError
	if errors.As(err, &de) {
		return de.Kind
	}
	return FailureDefinite
}

// Channel 是发送侧的最小接口。
//
// @remarks 入站由连接层直接交给核心，不经过本接口。
type Channel interface {
	SendText(ctx context.Context, out Outbound) (Receipt, error)
}
