package dingtalk

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
)

func newTestChannel(t *testing.T, requireMention bool) (*Channel, *core.Engine) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := core.New(agent.Echo{}, nil, logger)
	c := New(Config{
		ClientID: "id", ClientSecret: "secret",
		RequireMention: requireMention, BotName: "助手",
		ModelName: "test-model", ModelBaseURL: "https://gw/v1",
	}, eng, logger)
	return c, eng
}

func callback(conv, staff, text string) *chatbot.BotCallbackDataModel {
	return &chatbot.BotCallbackDataModel{
		ConversationId:            conv,
		ConversationType:          "2",
		ConversationTitle:         "运维群",
		MsgId:                     "msg-" + text,
		SenderStaffId:             staff,
		SenderNick:                "张三",
		Text:                      chatbot.BotCallbackDataTextModel{Content: text},
		IsInAtList:                true,
		SessionWebhookExpiredTime: time.Now().Add(time.Hour).UnixMilli(),
	}
}

func TestOnMessageSubmitsInbound(t *testing.T) {
	c, eng := newTestChannel(t, true)
	if _, err := c.onMessage(context.Background(), callback("conv-1", "staff-1", "@助手 你好")); err != nil {
		t.Fatal(err)
	}
	msgs := eng.Messages("dingtalk:conv-1:staff-1")
	if len(msgs) != 1 || msgs[0].Text != "你好" {
		t.Fatalf("msgs = %+v", msgs)
	}
}

func TestGroupRequiresMention(t *testing.T) {
	c, eng := newTestChannel(t, true)
	data := callback("conv-2", "staff-2", "没有 at")
	data.IsInAtList = false
	if _, err := c.onMessage(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if got := len(eng.Messages("dingtalk:conv-2:staff-2")); got != 0 {
		t.Fatalf("未 @ 不应受理，得到 %d 条", got)
	}
}

func TestCommandRepliesAndDoesNotQueue(t *testing.T) {
	c, eng := newTestChannel(t, false)
	var replies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Text struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		_ = json.Unmarshal(raw, &body)
		replies = append(replies, body.Text.Content)
	}))
	defer srv.Close()

	data := callback("conv-3", "staff-3", "/status")
	data.SessionWebhook = srv.URL
	data.SessionWebhookExpiredTime = time.Now().Add(time.Hour).UnixMilli()
	if _, err := c.onMessage(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || !strings.Contains(replies[0], "排队") {
		t.Fatalf("replies = %+v", replies)
	}
	if got := len(eng.Messages("dingtalk:conv-3:staff-3")); got != 0 {
		t.Fatalf("命令不应入队，得到 %d 条", got)
	}
}

func TestSendTextUsesSessionWebhook(t *testing.T) {
	c, _ := newTestChannel(t, false)
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Text struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		_ = json.Unmarshal(raw, &body)
		got = body.Text.Content
	}))
	defer srv.Close()
	c.rememberHook("conv-x", srv.URL, time.Now().Add(time.Hour).UnixMilli(), "张三")

	if _, err := c.SendText(context.Background(), channel.Outbound{ChatID: "conv-x", Text: "结果"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "结果") {
		t.Fatalf("got = %q", got)
	}
}

func TestSendTextRejectsMissingHook(t *testing.T) {
	c, _ := newTestChannel(t, false)
	if _, err := c.SendText(context.Background(), channel.Outbound{ChatID: "nope", Text: "hi"}); err == nil {
		t.Fatal("没有 webhook 时应报错")
	}
}

func TestGroupOpenBypassesUserAllowlist(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := core.New(agent.Echo{}, nil, logger)
	c := New(Config{
		ClientID: "id", ClientSecret: "secret",
		AllowedUsers: []string{"staff-ok"}, GroupOpen: true,
		BotName: "助手", ModelName: "test-model", ModelBaseURL: "https://gw/v1",
	}, eng, logger)
	if _, err := c.onMessage(context.Background(), callback("conv-open", "staff-other", "@助手 你好")); err != nil {
		t.Fatal(err)
	}
	if got := len(eng.Messages("dingtalk:conv-open:staff-other")); got != 1 {
		t.Fatalf("群内免白名单应受理，得到 %d 条", got)
	}

	dm := callback("conv-dm", "staff-other", "@助手 你好")
	dm.ConversationType = "1"
	if _, err := c.onMessage(context.Background(), dm); err != nil {
		t.Fatal(err)
	}
	if got := len(eng.Messages("dingtalk:conv-dm:staff-other")); got != 0 {
		t.Fatalf("私聊仍应拒绝，得到 %d 条", got)
	}
}

func TestChatRecordContentBecomesReadableText(t *testing.T) {
	c, eng := newTestChannel(t, false)
	data := callback("conv-record", "staff-r", "")
	data.Msgtype = "chatRecord"
	data.Content = map[string]any{"title": "合并转发的聊天记录", "summary": "张三: 第一条\n李四: 第二条"}
	if _, err := c.onMessage(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	msgs := eng.Messages("dingtalk:conv-record:staff-r")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "第一条") || msgs[0].MsgType != "chatRecord" {
		t.Fatalf("msgs = %+v", msgs)
	}
}

func TestFileAttachmentDownload(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/oauth2/accessToken":
			_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "token", "expireIn": 7200})
		case "/v1.0/robot/messageFiles/download":
			_ = json.NewEncoder(w).Encode(map[string]any{"downloadUrl": srv.URL + "/file"})
		case "/file":
			_, _ = w.Write([]byte("attachment-body"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := core.New(agent.Echo{}, nil, logger)
	dir := t.TempDir()
	c := New(Config{
		ClientID: "id", ClientSecret: "secret", AttachmentsDir: dir, APIBase: srv.URL,
		ModelName: "test", ModelBaseURL: "https://gw/v1",
	}, eng, logger)
	data := callback("conv-file", "staff-f", "")
	data.MsgId = "msg-file"
	data.Msgtype = "file"
	data.Content = map[string]any{"downloadCode": "code", "fileName": "报告.txt"}
	if _, err := c.onMessage(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	msgs := eng.Messages("dingtalk:conv-file:staff-f")
	if len(msgs) != 1 || len(msgs[0].Attachments) != 1 {
		t.Fatalf("msgs = %+v", msgs)
	}
	body, err := os.ReadFile(msgs[0].Attachments[0].Path)
	if err != nil || string(body) != "attachment-body" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestSendArtifactUploadsToWebhook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "报告.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	var msgType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/gettoken":
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "t", "expires_in": 7200})
		case r.URL.Path == "/media/upload":
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "media_id": "media-1"})
		case r.URL.Path == "/webhook":
			var body struct {
				MsgType string `json:"msgtype"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			msgType = body.MsgType
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := core.New(agent.Echo{}, nil, logger)
	c := New(Config{ClientID: "id", ClientSecret: "secret", OAPIBase: srv.URL}, eng, logger)
	c.rememberHook("conv-file", srv.URL+"/webhook", time.Now().Add(time.Hour).UnixMilli(), "")
	receipt, err := c.SendArtifact(context.Background(), channel.OutboundArtifact{
		ChatID:   "conv-file",
		Artifact: channel.Artifact{Name: "报告.txt", Path: path, Kind: "file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ExternalID != "" || msgType != "file" {
		t.Fatalf("receipt=%+v msgType=%s", receipt, msgType)
	}
}
