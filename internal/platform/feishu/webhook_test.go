package feishu_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/platform/feishu"
)

type sentMessage struct {
	path  string
	reply bool
	body  map[string]any
}

type fakeAPI struct {
	mu   sync.Mutex
	sent []sentMessage
}

func (f *fakeAPI) messages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sentMessage, len(f.sent))
	copy(out, f.sent)
	return out
}

func newFakeAPI(t *testing.T) (*fakeAPI, *httptest.Server) {
	t.Helper()
	f := &fakeAPI{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok", "tenant_access_token": "t-test", "expire": 7200,
			})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/im/v1/messages/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"items": []any{map[string]any{
					"msg_type": "text",
					"body":     map[string]string{"content": `{"text":"被引用的原始消息"}`},
				}}},
			})
		case strings.Contains(r.URL.Path, "/resources/"):
			_, _ = w.Write([]byte("attachment-bytes"))
		case strings.Contains(r.URL.Path, "/im/v1/messages"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.sent = append(f.sent, sentMessage{
				path:  r.URL.Path,
				reply: strings.HasSuffix(r.URL.Path, "/reply"),
				body:  body,
			})
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok", "data": map[string]string{"message_id": "om_out_1"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func messageJSON(eventID, messageID, chatID, chatType, text string, mentionBot bool) string {
	content, _ := json.Marshal(map[string]string{"text": text})
	mentions := ""
	if mentionBot {
		mentions = `,"mentions":[{"key":"@_user_1","name":"机器人","id":{"open_id":"ou_bot"}}]`
	}
	return fmt.Sprintf(`{
	  "schema":"2.0",
	  "header":{"event_id":"%s","event_type":"im.message.receive_v1","tenant_key":"tenant_1","app_id":"cli_test","token":"vt","create_time":"1700000000000"},
	  "event":{
	    "sender":{"sender_id":{"open_id":"ou_user"},"sender_type":"user"},
	    "message":{"message_id":"%s","chat_id":"%s","chat_type":"%s","message_type":"text","content":%q,"create_time":"1700000000000"%s}
	  }
	}`, eventID, messageID, chatID, chatType, string(content), mentions)
}

func newHook(t *testing.T) (*feishu.Webhook, *core.Engine, *fakeAPI) {
	t.Helper()
	hook, engine, api, _ := newHookWithDir(t)
	return hook, engine, api
}

func newHookWithDir(t *testing.T) (*feishu.Webhook, *core.Engine, *fakeAPI, string) {
	t.Helper()
	api, srv := newFakeAPI(t)
	client := feishu.NewClient("cli_test", "secret", feishu.WithBaseURL(srv.URL))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := core.New(agent.Echo{}, feishu.NewChannel(client), logger)
	dispatcher := feishu.NewDispatcher(engine, feishu.Options{
		BotOpenID:      "ou_bot",
		RequireMention: true,
		AllowedChats:   []string{"oc_1", "oc_group"},
	}, logger)
	attachments := t.TempDir()
	dispatcher = feishu.NewDispatcher(engine, feishu.Options{
		BotOpenID:      "ou_bot",
		RequireMention: true,
		AllowedChats:   []string{"oc_1", "oc_group"},
		Client:         client,
		AttachmentsDir: attachments,
	}, logger)
	hook := feishu.NewWebhook(dispatcher, "vt", logger)
	return hook, engine, api, attachments
}

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/feishu/events", bytes.NewBufferString(body))
	h.ServeHTTP(rec, req)
	return rec
}

func TestURLVerification(t *testing.T) {
	hook, _, _ := newHook(t)
	rec := post(t, hook, `{"type":"url_verification","challenge":"abc123","token":"vt"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "abc123") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestMessageEndToEnd(t *testing.T) {
	hook, engine, api := newHook(t)
	ctx := context.Background()

	rec := post(t, hook, messageJSON("ev_1", "om_1", "oc_1", "p2p", "你好", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if err := engine.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	sent := api.messages()
	if len(sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(sent))
	}
	if !sent[0].reply {
		t.Fatal("应使用回复接口建立回复关系")
	}
	if !strings.Contains(sent[0].path, "/om_1/reply") {
		t.Fatalf("reply path = %s", sent[0].path)
	}
	content, _ := sent[0].body["content"].(string)
	if !strings.Contains(content, "你好") {
		t.Fatalf("content = %s", content)
	}
}

func TestDuplicateEventDeliversOnce(t *testing.T) {
	hook, engine, api := newHook(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		post(t, hook, messageJSON("ev_dup", "om_dup", "oc_1", "p2p", "重复", false))
		if err := engine.Flush(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(api.messages()); got != 1 {
		t.Fatalf("重复事件投递了 %d 次", got)
	}
}

func TestGroupWithoutMentionIsIgnored(t *testing.T) {
	hook, engine, api := newHook(t)
	ctx := context.Background()

	post(t, hook, messageJSON("ev_g1", "om_g1", "oc_group", "group", "大家早", false))
	if err := engine.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(api.messages()); got != 0 {
		t.Fatalf("未 @机器人 的群消息不应处理，实际投递 %d 次", got)
	}

	post(t, hook, messageJSON("ev_g2", "om_g2", "oc_group", "group", "@_user_1 看一下", true))
	if err := engine.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(api.messages()); got != 1 {
		t.Fatalf("@机器人 的群消息应处理，实际投递 %d 次", got)
	}
}

func TestRejectsWrongToken(t *testing.T) {
	hook, engine, api := newHook(t)
	body := strings.Replace(messageJSON("ev_t", "om_t", "oc_1", "p2p", "hi", false), `"token":"vt"`, `"token":"bad"`, 1)
	rec := post(t, hook, body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	_ = engine.Flush(context.Background())
	if len(api.messages()) != 0 {
		t.Fatal("非法来源不应产生投递")
	}
}

func TestChatAllowlist(t *testing.T) {
	hook, engine, api := newHook(t)
	post(t, hook, messageJSON("ev_x", "om_x", "oc_unknown", "p2p", "hi", false))
	_ = engine.Flush(context.Background())
	if len(api.messages()) != 0 {
		t.Fatal("不在白名单的会话不应处理")
	}
}

func imageEventJSON(eventID, messageID, chatID, sender string) string {
	return fmt.Sprintf(`{
	  "schema":"2.0",
	  "header":{"event_id":"%s","event_type":"im.message.receive_v1","tenant_key":"tenant_1","app_id":"cli_test","token":"vt","create_time":"1700000000000"},
	  "event":{
	    "sender":{"sender_id":{"open_id":"%s"},"sender_type":"user"},
	    "message":{"message_id":"%s","chat_id":"%s","chat_type":"p2p","message_type":"image","content":"{\"image_key\":\"img_v3_test\"}","create_time":"1700000000000"}
	  }
	}`, eventID, sender, messageID, chatID)
}

func TestImageAttachmentDownloadedBeforeSubmit(t *testing.T) {
	hook, engine, _, dir := newHookWithDir(t)
	post(t, hook, imageEventJSON("ev_img", "om_img", "oc_1", "ou_user"))

	var msgs []*core.Message
	for i := 0; i < 60; i++ {
		msgs = engine.Messages("tenant_1:oc_1:ou_user")
		if len(msgs) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(msgs))
	}
	if len(msgs[0].Attachments) != 1 || msgs[0].Attachments[0].Path == "" {
		t.Fatalf("attachment 未落盘: %+v", msgs[0].Attachments)
	}
	raw, err := os.ReadFile(msgs[0].Attachments[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "attachment-bytes" {
		t.Fatalf("content = %q", raw)
	}
	if !strings.HasPrefix(msgs[0].Attachments[0].Path, dir) {
		t.Fatalf("path 不在附件目录: %s", msgs[0].Attachments[0].Path)
	}
}

func TestCommandStatusRepliesWithoutQueueing(t *testing.T) {
	hook, engine, api := newHook(t)
	post(t, hook, messageJSON("ev_cmd", "om_cmd", "oc_1", "p2p", "/status", false))

	sent := api.messages()
	if len(sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(sent))
	}
	content, _ := sent[0].body["content"].(string)
	if !strings.Contains(content, "当前状态") {
		t.Fatalf("content = %s", content)
	}
	if msgs := engine.Messages("tenant_1:oc_1:ou_user"); len(msgs) != 0 {
		t.Fatalf("命令不应入队，实际 %d", len(msgs))
	}
}

func TestCommandUnknownRepliesHelp(t *testing.T) {
	hook, _, api := newHook(t)
	post(t, hook, messageJSON("ev_cmd2", "om_cmd2", "oc_1", "p2p", "/nope", false))
	sent := api.messages()
	if len(sent) != 1 {
		t.Fatalf("sent = %d", len(sent))
	}
	content, _ := sent[0].body["content"].(string)
	if !strings.Contains(content, "/help") {
		t.Fatalf("content = %s", content)
	}
}

func replyEventJSON(eventID, messageID, chatID, parentID string, text string) string {
	content, _ := json.Marshal(map[string]string{"text": text})
	return fmt.Sprintf(`{
	  "schema":"2.0",
	  "header":{"event_id":"%s","event_type":"im.message.receive_v1","tenant_key":"tenant_1","app_id":"cli_test","token":"vt","create_time":"1700000000000"},
	  "event":{
	    "sender":{"sender_id":{"open_id":"ou_user"},"sender_type":"user"},
	    "message":{"message_id":"%s","chat_id":"%s","chat_type":"p2p","message_type":"text","content":%q,"parent_id":"%s","create_time":"1700000000000"}
	  }
	}`, eventID, messageID, chatID, string(content), parentID)
}

func TestQuotedMessageIsAddedToPrompt(t *testing.T) {
	hook, engine, _ := newHook(t)
	post(t, hook, replyEventJSON("ev_q", "om_q", "oc_1", "om_parent", "这个结论对吗？"))

	msgs := engine.Messages("tenant_1:oc_1:ou_user")
	if len(msgs) != 1 {
		t.Fatalf("messages = %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Text, "被引用的原始消息") || !strings.Contains(msgs[0].Text, "这个结论对吗") {
		t.Fatalf("text = %q", msgs[0].Text)
	}
}
