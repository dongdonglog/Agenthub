// Package feishu 实现飞书通道的协议转换、发送与事件入口。
package feishu

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/channel"
)

// Header 是飞书事件头。
type Header struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	CreateTime string `json:"create_time"`
	Token      string `json:"token"`
	AppID      string `json:"app_id"`
	TenantKey  string `json:"tenant_key"`
}

// Mention 是一次 @。
type Mention struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	ID   struct {
		OpenID  string `json:"open_id"`
		UnionID string `json:"union_id"`
		UserID  string `json:"user_id"`
	} `json:"id"`
}

// Message 是飞书消息体。
type Message struct {
	MessageID   string    `json:"message_id"`
	ChatID      string    `json:"chat_id"`
	ChatType    string    `json:"chat_type"`
	MessageType string    `json:"message_type"`
	Content     string    `json:"content"`
	ParentID    string    `json:"parent_id"`
	RootID      string    `json:"root_id"`
	CreateTime  string    `json:"create_time"`
	Mentions    []Mention `json:"mentions"`
}

// EventBody 是事件正文。
type EventBody struct {
	Sender struct {
		SenderID struct {
			OpenID  string `json:"open_id"`
			UnionID string `json:"union_id"`
			UserID  string `json:"user_id"`
		} `json:"sender_id"`
		SenderType string `json:"sender_type"`
	} `json:"sender"`
	Message Message `json:"message"`
}

// Payload 是事件回调的完整载荷。
type Payload struct {
	Type      string    `json:"type"`
	Challenge string    `json:"challenge"`
	Token     string    `json:"token"`
	Schema    string    `json:"schema"`
	Header    Header    `json:"header"`
	Event     EventBody `json:"event"`
}

// IsURLVerification 判断是否为回调地址校验请求。
func (p *Payload) IsURLVerification() bool {
	return p.Type == "url_verification" || p.Challenge != ""
}

// ParseMessage 把消息事件转换为标准入站消息。
//
// @param botOpenID 机器人自身的 open_id；为空时把任意 @ 视为 @机器人
// @returns ok 为 false 表示不是需要处理的消息事件（非消息事件、机器人自己发的）
func ParseMessage(p *Payload, botOpenID string) (channel.Inbound, bool) {
	if p.Header.EventType != "im.message.receive_v1" {
		return channel.Inbound{}, false
	}
	if p.Event.Sender.SenderType == "app" {
		return channel.Inbound{}, false
	}
	msg := p.Event.Message
	if msg.MessageID == "" || msg.ChatID == "" {
		return channel.Inbound{}, false
	}

	text, attachments := extractContent(msg)
	mentionsBot := false
	if len(msg.Mentions) > 0 {
		if botOpenID == "" {
			mentionsBot = true
		} else {
			for _, m := range msg.Mentions {
				if m.ID.OpenID == botOpenID {
					mentionsBot = true
					break
				}
			}
		}
	}
	if msg.ChatType == "p2p" {
		mentionsBot = true
	}

	tenant := p.Header.TenantKey
	if tenant == "" {
		tenant = p.Header.AppID
	}

	return channel.Inbound{
		Channel:      "feishu",
		Tenant:       tenant,
		ExternalID:   msg.MessageID,
		SentAt:       parseMillis(msg.CreateTime),
		Sender:       channel.Sender{OpenID: p.Event.Sender.SenderID.OpenID},
		Chat:         channel.Chat{ID: msg.ChatID, Kind: msg.ChatType, Name: ""},
		Text:         text,
		MsgType:      msg.MessageType,
		MentionsBot:  mentionsBot,
		ParentID:     msg.ParentID,
		Attachments:  attachments,
		RawEventType: p.Header.EventType,
	}, true
}

func extractContent(msg Message) (string, []channel.Attachment) {
	switch msg.MessageType {
	case "text":
		var c struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(msg.Content), &c); err != nil {
			return "", nil
		}
		return stripMentions(c.Text, msg.Mentions), nil

	case "post":
		return parsePost(msg.Content), nil

	case "image":
		var c struct {
			ImageKey string `json:"image_key"`
		}
		_ = json.Unmarshal([]byte(msg.Content), &c)
		return "[图片]", []channel.Attachment{{Kind: "image", FileKey: c.ImageKey}}

	case "file":
		var c struct {
			FileKey  string `json:"file_key"`
			FileName string `json:"file_name"`
		}
		_ = json.Unmarshal([]byte(msg.Content), &c)
		return fmt.Sprintf("[文件：%s]", c.FileName), []channel.Attachment{{Kind: "file", FileKey: c.FileKey, Name: c.FileName}}

	case "audio":
		var c struct {
			FileKey string `json:"file_key"`
		}
		_ = json.Unmarshal([]byte(msg.Content), &c)
		return "[语音]", []channel.Attachment{{Kind: "audio", FileKey: c.FileKey}}

	case "media":
		var c struct {
			FileKey  string `json:"file_key"`
			FileName string `json:"file_name"`
		}
		_ = json.Unmarshal([]byte(msg.Content), &c)
		return "[视频]", []channel.Attachment{{Kind: "media", FileKey: c.FileKey, Name: c.FileName}}

	case "sticker":
		var c struct {
			FileKey string `json:"file_key"`
		}
		_ = json.Unmarshal([]byte(msg.Content), &c)
		return "[表情包]", []channel.Attachment{{Kind: "image", FileKey: c.FileKey}}

	default:
		return fmt.Sprintf("[暂不支持的消息类型：%s]", msg.MessageType), nil
	}
}

type postBody struct {
	Title   string          `json:"title"`
	Content [][]postElement `json:"content"`
}

type postElement struct {
	Tag    string `json:"tag"`
	Text   string `json:"text"`
	Href   string `json:"href"`
	UserID string `json:"user_id"`
}

func parsePost(raw string) string {
	var p postBody
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return "[富文本]"
	}
	var b strings.Builder
	if p.Title != "" {
		b.WriteString(p.Title)
		b.WriteString("\n")
	}
	for _, line := range p.Content {
		for _, el := range line {
			switch el.Tag {
			case "text":
				b.WriteString(el.Text)
			case "a":
				b.WriteString(el.Text)
				if el.Href != "" {
					b.WriteString("(" + el.Href + ")")
				}
			case "at":
				if el.UserID != "" {
					b.WriteString("@")
				}
			}
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func stripMentions(text string, mentions []Mention) string {
	for _, m := range mentions {
		if m.Key != "" {
			text = strings.ReplaceAll(text, m.Key, "")
		}
	}
	return strings.TrimSpace(text)
}

func parseMillis(raw string) time.Time {
	if raw == "" {
		return time.Now()
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Now()
	}
	return time.UnixMilli(ms)
}
