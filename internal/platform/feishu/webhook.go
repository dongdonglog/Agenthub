package feishu

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
)

// WebhookOptions 是事件入口的策略配置。
type WebhookOptions struct {
	// VerificationToken 非空时校验事件来源。
	VerificationToken string
	// BotOpenID 是机器人自身 open_id；为空时把任意 @ 视为 @机器人。
	BotOpenID string
	// RequireMention 为 true 时群聊必须 @机器人。
	RequireMention bool
	// AllowedUsers 非空时只接受列表内的 open_id。
	AllowedUsers []string
	// AllowedChats 非空时只接受列表内的 chat_id。
	AllowedChats []string
}

// Webhook 处理飞书事件回调。
type Webhook struct {
	engine       *core.Engine
	opts         WebhookOptions
	log          *slog.Logger
	allowedUsers map[string]struct{}
	allowedChats map[string]struct{}
}

// NewWebhook 构造事件入口。
func NewWebhook(engine *core.Engine, opts WebhookOptions, log *slog.Logger) *Webhook {
	if log == nil {
		log = slog.Default()
	}
	w := &Webhook{
		engine:       engine,
		opts:         opts,
		log:          log,
		allowedUsers: toSet(opts.AllowedUsers),
		allowedChats: toSet(opts.AllowedChats),
	}
	return w
}

// ServeHTTP 处理一次事件回调。
//
// @remarks 必须在 3 秒内返回；受理只做去重与入队，执行异步进行。
func (w *Webhook) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(rw, "bad request", http.StatusBadRequest)
		return
	}

	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		http.Error(rw, "bad json", http.StatusBadRequest)
		return
	}

	if w.opts.VerificationToken != "" && !w.tokenOK(&p) {
		w.log.Warn("feishu webhook token mismatch")
		http.Error(rw, "forbidden", http.StatusForbidden)
		return
	}

	if p.IsURLVerification() {
		writeJSON(rw, http.StatusOK, map[string]string{"challenge": p.Challenge})
		return
	}

	msg, ok := ParseMessage(&p, w.opts.BotOpenID)
	if !ok {
		writeJSON(rw, http.StatusOK, map[string]int{"code": 0})
		return
	}
	if !w.allowed(msg) {
		w.log.Info("feishu message rejected by policy",
			"chat", msg.Chat.ID, "sender", msg.Sender.OpenID, "kind", msg.Chat.Kind)
		writeJSON(rw, http.StatusOK, map[string]int{"code": 0})
		return
	}

	if _, err := w.engine.Submit(r.Context(), msg); err != nil {
		w.log.Error("submit failed", "error", err, "message", msg.ExternalID)
		http.Error(rw, "submit failed", http.StatusInternalServerError)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]int{"code": 0})
}

func (w *Webhook) tokenOK(p *Payload) bool {
	token := p.Header.Token
	if token == "" {
		token = p.Token
	}
	return token == w.opts.VerificationToken
}

func (w *Webhook) allowed(msg channel.Inbound) bool {
	if len(w.allowedChats) > 0 {
		if _, ok := w.allowedChats[msg.Chat.ID]; !ok {
			return false
		}
	}
	if len(w.allowedUsers) > 0 {
		if _, ok := w.allowedUsers[msg.Sender.OpenID]; !ok {
			return false
		}
	}
	if w.opts.RequireMention && msg.Chat.Kind != "p2p" && !msg.MentionsBot {
		return false
	}
	return true
}

func writeJSON(rw http.ResponseWriter, status int, body any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(body)
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
