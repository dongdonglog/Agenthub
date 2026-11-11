package feishu

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
)

// Options 是事件受理策略。
//
// @remarks 长连接与 webhook 两种传输共用同一套策略，避免行为分叉。
type Options struct {
	// BotOpenID 是机器人自身 open_id；为空时把任意 @ 视为 @机器人。
	BotOpenID string
	// RequireMention 为 true 时群聊必须 @机器人。
	RequireMention bool
	// AllowedUsers 非空时只接受列表内的 open_id。
	AllowedUsers []string
	// AllowedChats 非空时只接受列表内的 chat_id。
	AllowedChats []string
}

// Dispatcher 把飞书事件转换成标准消息并交给核心。
//
// @remarks 只做解析、策略与受理；执行与投递由核心异步完成，
// 因此可以满足事件 3 秒内确认的要求。
type Dispatcher struct {
	engine       *core.Engine
	opts         Options
	log          *slog.Logger
	allowedUsers map[string]struct{}
	allowedChats map[string]struct{}
}

// NewDispatcher 构造事件分发器。
func NewDispatcher(engine *core.Engine, opts Options, log *slog.Logger) *Dispatcher {
	if log == nil {
		log = slog.Default()
	}
	return &Dispatcher{
		engine:       engine,
		opts:         opts,
		log:          log,
		allowedUsers: toSet(opts.AllowedUsers),
		allowedChats: toSet(opts.AllowedChats),
	}
}

// HandlePayload 处理一份原始事件 JSON。
//
// @remarks 长连接与 webhook 都走这里；非消息事件返回 nil。
// @throws JSON 非法或受理失败时返回错误
func (d *Dispatcher) HandlePayload(ctx context.Context, raw []byte) error {
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	return d.HandleEvent(ctx, &p)
}

// HandleEvent 处理已解析的事件。
func (d *Dispatcher) HandleEvent(ctx context.Context, p *Payload) error {
	msg, ok := ParseMessage(p, d.opts.BotOpenID)
	if !ok {
		return nil
	}
	if !d.allowed(msg) {
		d.log.Info("feishu message rejected by policy",
			"chat", msg.Chat.ID, "sender", msg.Sender.OpenID, "kind", msg.Chat.Kind)
		return nil
	}
	_, err := d.engine.Submit(ctx, msg)
	return err
}

func (d *Dispatcher) allowed(msg channel.Inbound) bool {
	if len(d.allowedChats) > 0 {
		if _, ok := d.allowedChats[msg.Chat.ID]; !ok {
			return false
		}
	}
	if len(d.allowedUsers) > 0 {
		if _, ok := d.allowedUsers[msg.Sender.OpenID]; !ok {
			return false
		}
	}
	if d.opts.RequireMention && msg.Chat.Kind != "p2p" && !msg.MentionsBot {
		return false
	}
	return true
}

func toSet(items []string) map[string]struct{} {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item != "" {
			out[item] = struct{}{}
		}
	}
	return out
}
