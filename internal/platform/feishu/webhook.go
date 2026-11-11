package feishu

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
)

// Webhook 处理飞书事件回调（将事件发送至开发者服务器）。
//
// @remarks 长连接模式不需要本入口；两种入口共用 {@link Dispatcher} 的策略。
type Webhook struct {
	dispatcher        *Dispatcher
	verificationToken string
	log               *slog.Logger
}

// NewWebhook 构造回调入口。
//
// @param verificationToken 非空时校验事件来源；为空表示不校验（仅限内网调试）
func NewWebhook(dispatcher *Dispatcher, verificationToken string, log *slog.Logger) *Webhook {
	if log == nil {
		log = slog.Default()
	}
	return &Webhook{dispatcher: dispatcher, verificationToken: verificationToken, log: log}
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

	if w.verificationToken != "" && !verifyToken(&p, w.verificationToken) {
		w.log.Warn("feishu webhook token mismatch")
		http.Error(rw, "forbidden", http.StatusForbidden)
		return
	}

	if p.IsURLVerification() {
		writeJSON(rw, http.StatusOK, map[string]string{"challenge": p.Challenge})
		return
	}

	if err := w.dispatcher.HandleEvent(r.Context(), &p); err != nil {
		w.log.Error("handle event failed", "error", err, "message", p.Event.Message.MessageID)
		http.Error(rw, "handle failed", http.StatusInternalServerError)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]int{"code": 0})
}

func verifyToken(p *Payload, want string) bool {
	token := p.Header.Token
	if token == "" {
		token = p.Token
	}
	return token == want
}

func writeJSON(rw http.ResponseWriter, status int, body any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(body)
}
