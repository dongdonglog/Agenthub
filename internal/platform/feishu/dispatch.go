package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"time"

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
	// Client 用于下载附件；为空表示不下载。
	Client *Client
	// AttachmentsDir 是附件落盘根目录。
	AttachmentsDir string
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
		d.log.Warn("feishu message rejected by policy: 把 sender 加入 FEISHU_ALLOWED_USERS 后重启",
			"chat", msg.Chat.ID, "sender", msg.Sender.OpenID, "kind", msg.Chat.Kind)
		return nil
	}
	if len(msg.Attachments) > 0 && d.opts.Client != nil && d.opts.AttachmentsDir != "" {
		// 附件下载可能超过事件确认窗口，放到后台完成后再入队。
		go func(m channel.Inbound) {
			dlCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			d.fetchAttachments(dlCtx, &m)
			if _, err := d.engine.Submit(context.Background(), m); err != nil {
				d.log.Error("submit with attachments failed", "error", err, "message", m.ExternalID)
			}
		}(msg)
		return nil
	}

	inserted, err := d.engine.Submit(ctx, msg)
	if err != nil {
		return err
	}
	if inserted {
		d.log.Info("feishu message accepted",
			"message", msg.ExternalID, "chat", msg.Chat.ID, "kind", msg.Chat.Kind,
			"sender", msg.Sender.OpenID, "type", msg.MsgType, "chars", len(msg.Text))
	} else {
		d.log.Info("feishu duplicate message ignored", "message", msg.ExternalID)
	}
	return nil
}

var unsafePath = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// fetchAttachments 把附件流式下载到本地并回填路径。
//
// @remarks 单个附件失败只记录日志，消息仍会入队；模型会看到"未下载成功"。
func (d *Dispatcher) fetchAttachments(ctx context.Context, msg *channel.Inbound) {
	dir := filepath.Join(d.opts.AttachmentsDir, unsafePath.ReplaceAllString(msg.ExternalID, "_"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		d.log.Error("create attachment dir failed", "error", err)
		return
	}
	for i := range msg.Attachments {
		a := &msg.Attachments[i]
		if a.FileKey == "" {
			continue
		}
		name := unsafePath.ReplaceAllString(a.Name, "_")
		if name == "" {
			name = unsafePath.ReplaceAllString(a.FileKey, "_")
		}
		path := filepath.Join(dir, fmt.Sprintf("%d_%s", i, name))
		file, err := os.Create(path)
		if err != nil {
			d.log.Error("create attachment failed", "error", err, "path", path)
			continue
		}
		n, err := d.opts.Client.DownloadResource(ctx, msg.ExternalID, a.FileKey, a.Kind, file)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			_ = os.Remove(path)
			d.log.Warn("download attachment failed", "message", msg.ExternalID, "file", a.Name, "error", err)
			continue
		}
		a.Path = path
		a.Size = n
		d.log.Info("attachment saved", "message", msg.ExternalID, "name", a.Name, "bytes", n)
	}
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
