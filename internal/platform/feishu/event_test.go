package feishu_test

import (
	"encoding/json"
	"testing"

	"github.com/moying/agenthub/internal/platform/feishu"
)

func textEvent(id, chat, sender, text string) *feishu.Payload {
	content, _ := json.Marshal(map[string]string{"text": text})
	return &feishu.Payload{
		Schema: "2.0",
		Header: feishu.Header{
			EventID:    "ev_" + id,
			EventType:  "im.message.receive_v1",
			TenantKey:  "tenant_1",
			AppID:      "cli_app",
			CreateTime: "1700000000000",
		},
		Event: feishu.EventBody{
			Sender: struct {
				SenderID struct {
					OpenID  string `json:"open_id"`
					UnionID string `json:"union_id"`
					UserID  string `json:"user_id"`
				} `json:"sender_id"`
				SenderType string `json:"sender_type"`
			}{SenderType: "user"},
			Message: feishu.Message{
				MessageID:   id,
				ChatID:      chat,
				ChatType:    "p2p",
				MessageType: "text",
				Content:     string(content),
				CreateTime:  "1700000000000",
			},
		},
		// 结构体匿名成员无法直接赋值，测试里用下面这个辅助函数补全。
	}
}

func TestParseTextMessage(t *testing.T) {
	p := textEvent("om_1", "oc_1", "ou_1", "你好")
	p.Event.Sender.SenderID.OpenID = "ou_1"

	msg, ok := feishu.ParseMessage(p, "ou_bot")
	if !ok {
		t.Fatal("expected message to be parsed")
	}
	if msg.ExternalID != "om_1" || msg.Chat.ID != "oc_1" {
		t.Fatalf("unexpected ids: %+v", msg)
	}
	if msg.Text != "你好" {
		t.Fatalf("text = %q", msg.Text)
	}
	if msg.Tenant != "tenant_1" {
		t.Fatalf("tenant = %q", msg.Tenant)
	}
	if !msg.MentionsBot {
		t.Fatal("p2p 消息应视为 @机器人")
	}
}

func TestParseGroupNeedsMention(t *testing.T) {
	p := textEvent("om_2", "oc_2", "ou_2", "大家好")
	p.Event.Message.ChatType = "group"

	msg, ok := feishu.ParseMessage(p, "ou_bot")
	if !ok {
		t.Fatal("expected parsed")
	}
	if msg.MentionsBot {
		t.Fatal("群消息未 @机器人时不应标记")
	}
}

func TestParseGroupWithBotMention(t *testing.T) {
	p := textEvent("om_3", "oc_3", "ou_3", "@_user_1 帮忙看看")
	p.Event.Message.ChatType = "group"
	p.Event.Message.Mentions = []feishu.Mention{{
		Key: "@_user_1",
		ID: struct {
			OpenID  string `json:"open_id"`
			UnionID string `json:"union_id"`
			UserID  string `json:"user_id"`
		}{OpenID: "ou_bot"},
	}}

	msg, ok := feishu.ParseMessage(p, "ou_bot")
	if !ok {
		t.Fatal("expected parsed")
	}
	if !msg.MentionsBot {
		t.Fatal("应识别 @机器人")
	}
	if msg.Text != "帮忙看看" {
		t.Fatalf("mention placeholder 未清理: %q", msg.Text)
	}
}

func TestParseImageAndFile(t *testing.T) {
	img := textEvent("om_4", "oc_4", "ou_4", "")
	img.Event.Message.MessageType = "image"
	img.Event.Message.Content = `{"image_key":"img_v3_abc"}`

	msg, ok := feishu.ParseMessage(img, "ou_bot")
	if !ok || len(msg.Attachments) != 1 || msg.Attachments[0].FileKey != "img_v3_abc" {
		t.Fatalf("image 解析失败: %+v %v", msg, ok)
	}

	file := textEvent("om_5", "oc_5", "ou_5", "")
	file.Event.Message.MessageType = "file"
	file.Event.Message.Content = `{"file_key":"file_v3_xyz","file_name":"日志.mcap"}`

	msg, ok = feishu.ParseMessage(file, "ou_bot")
	if !ok || len(msg.Attachments) != 1 || msg.Attachments[0].Name != "日志.mcap" {
		t.Fatalf("file 解析失败: %+v", msg)
	}
}

func TestParsePost(t *testing.T) {
	p := textEvent("om_6", "oc_6", "ou_6", "")
	p.Event.Message.MessageType = "post"
	p.Event.Message.Content = `{"title":"周报","content":[[{"tag":"text","text":"本周完成："},{"tag":"text","text":"上线"}] ,[{"tag":"a","text":"看板","href":"https://example.com"}]]}`

	msg, ok := feishu.ParseMessage(p, "ou_bot")
	if !ok {
		t.Fatal("expected parsed")
	}
	if msg.Text != "周报\n本周完成：上线\n看板(https://example.com)" {
		t.Fatalf("post 文本 = %q", msg.Text)
	}
}

func TestParseIgnoresBotAndOtherEvents(t *testing.T) {
	p := textEvent("om_7", "oc_7", "ou_7", "hi")
	p.Event.Sender.SenderType = "app"
	if _, ok := feishu.ParseMessage(p, ""); ok {
		t.Fatal("机器人自己的消息应忽略")
	}

	p = textEvent("om_8", "oc_8", "ou_8", "hi")
	p.Header.EventType = "im.chat.updated_v1"
	if _, ok := feishu.ParseMessage(p, ""); ok {
		t.Fatal("非消息事件应忽略")
	}
}

func TestParseUnsupportedTypeBecomesPlaceholder(t *testing.T) {
	p := textEvent("om_9", "oc_9", "ou_9", "")
	p.Event.Message.MessageType = "merge_forward"
	p.Event.Message.Content = `{"text":"x"}`

	msg, ok := feishu.ParseMessage(p, "ou_bot")
	if !ok {
		t.Fatal("expected parsed")
	}
	if msg.Text == "" {
		t.Fatal("不支持的类型应给出占位说明")
	}
}
