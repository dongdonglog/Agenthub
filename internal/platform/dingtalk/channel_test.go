package dingtalk

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	c.rememberHook("conv-x", srv.URL, time.Now().Add(time.Hour).UnixMilli())

	if _, err := c.SendText(context.Background(), channel.Outbound{ChatID: "conv-x", Text: "结果"}); err != nil {
		t.Fatal(err)
	}
	if got != "结果" {
		t.Fatalf("got = %q", got)
	}
}

func TestSendTextRejectsMissingHook(t *testing.T) {
	c, _ := newTestChannel(t, false)
	if _, err := c.SendText(context.Background(), channel.Outbound{ChatID: "nope", Text: "hi"}); err == nil {
		t.Fatal("没有 webhook 时应报错")
	}
}
