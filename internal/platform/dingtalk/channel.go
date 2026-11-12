// Package dingtalk 实现钉钉机器人 Stream 模式接入。
package dingtalk

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/client"

	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
)

// Config 是钉钉接入配置。
type Config struct {
	ClientID       string
	ClientSecret   string
	RequireMention bool
	AllowedUsers   []string
	AllowedChats   []string
	BotName        string
	ModelName      string
	ModelBaseURL   string
}

// Channel 同时承担 Stream 连接与发送。
type Channel struct {
	cfg    Config
	engine *core.Engine
	log    *slog.Logger
	cli    *client.StreamClient

	mu    sync.Mutex
	hooks map[string]webhook // conversationID -> session webhook
}

type webhook struct {
	url      string
	expireAt time.Time
	userName string
}

// New 构造钉钉通道。
func New(cfg Config, engine *core.Engine, log *slog.Logger) *Channel {
	if log == nil {
		log = slog.Default()
	}
	return &Channel{cfg: cfg, engine: engine, log: log, hooks: make(map[string]webhook)}
}

// SetEngine 注入核心引擎（构造顺序所限，连接建立前调用）。
func (c *Channel) SetEngine(engine *core.Engine) {
	c.engine = engine
}

// Start 建立 Stream 长连接并阻塞直到 ctx 结束。
func (c *Channel) Start(ctx context.Context) error {
	if c.cfg.ClientID == "" || c.cfg.ClientSecret == "" {
		return errors.New("缺少 DINGTALK_CLIENT_ID / DINGTALK_CLIENT_SECRET")
	}
	cli := client.NewStreamClient(
		client.WithAppCredential(client.NewAppCredentialConfig(c.cfg.ClientID, c.cfg.ClientSecret)),
		client.WithAutoReconnect(true),
	)
	cli.RegisterChatBotCallbackRouter(c.onMessage)
	c.cli = cli
	c.log.Info("dingtalk stream starting")
	if err := cli.Start(ctx); err != nil {
		return fmt.Errorf("钉钉长连接退出: %w", err)
	}
	return nil
}

// onMessage 处理机器人回调。
func (c *Channel) onMessage(ctx context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error) {
	if data == nil {
		return nil, nil
	}
	text := strings.TrimSpace(data.Text.Content)
	if text == "" {
		return nil, nil
	}
	if c.cfg.BotName != "" {
		text = strings.TrimSpace(strings.TrimPrefix(text, "@"+c.cfg.BotName))
	}

	kind := "p2p"
	if data.ConversationType == "2" {
		kind = "group"
	}
	if kind == "group" && c.cfg.RequireMention && !data.IsInAtList {
		c.log.Debug("dingtalk group message without mention ignored", "conversation", data.ConversationId)
		return nil, nil
	}
	sender := data.SenderStaffId
	if sender == "" {
		sender = data.SenderId
	}
	if !c.allowed(sender, data.ConversationId) {
		c.log.Debug("dingtalk message rejected by allowlist", "sender", sender, "conversation", data.ConversationId)
		return nil, nil
	}

	conv := "dingtalk:" + data.ConversationId + ":" + sender
	c.rememberHook(data.ConversationId, data.SessionWebhook, data.SessionWebhookExpiredTime, data.SenderNick)
	c.engine.SetIdentity(conv, data.ConversationTitle, data.SenderNick)

	if c.handleCommand(ctx, conv, data.ConversationId, text) {
		return nil, nil
	}

	if _, err := c.engine.Submit(ctx, channel.Inbound{
		Channel:     "dingtalk",
		Tenant:      "dingtalk",
		ExternalID:  data.MsgId,
		SentAt:      time.UnixMilli(data.CreateAt),
		Sender:      channel.Sender{OpenID: sender, Name: data.SenderNick},
		Chat:        channel.Chat{ID: data.ConversationId, Kind: kind, Name: data.ConversationTitle},
		Text:        text,
		MsgType:     "text",
		MentionsBot: data.IsInAtList,
	}); err != nil {
		if errors.Is(err, core.ErrRateLimited) {
			_, _ = c.SendText(ctx, channel.Outbound{ChatID: data.ConversationId, Text: "请求太频繁，请稍后再试。"})
			return nil, nil
		}
		c.log.Error("dingtalk submit failed", "error", err)
	}
	return nil, nil
}

// SendText 通过会话 webhook 回复。
func (c *Channel) SendText(ctx context.Context, out channel.Outbound) (channel.Receipt, error) {
	c.mu.Lock()
	hook, ok := c.hooks[out.ChatID]
	c.mu.Unlock()
	if !ok {
		return channel.Receipt{}, errors.New("没有可用的会话 webhook")
	}
	if time.Now().After(hook.expireAt.Add(-30 * time.Second)) {
		return channel.Receipt{}, errors.New("会话 webhook 已过期")
	}
	text := out.Text
	if hook.userName != "" && !strings.HasPrefix(text, "@"+hook.userName) {
		text = "@" + hook.userName + " " + text
	}
	replier := chatbot.NewChatbotReplier()
	if err := replier.SimpleReplyText(ctx, hook.url, []byte(text)); err != nil {
		return channel.Receipt{}, err
	}
	return channel.Receipt{}, nil
}

func (c *Channel) rememberHook(convID, url string, expireAtMs int64, userName string) {
	if url == "" {
		return
	}
	expire := time.UnixMilli(expireAtMs)
	if expireAtMs == 0 {
		expire = time.Now().Add(time.Hour)
	}
	c.mu.Lock()
	c.hooks[convID] = webhook{url: url, expireAt: expire, userName: userName}
	c.mu.Unlock()
}

func (c *Channel) allowed(sender, chatID string) bool {
	if len(c.cfg.AllowedUsers) > 0 && !contains(c.cfg.AllowedUsers, sender) {
		return false
	}
	if len(c.cfg.AllowedChats) > 0 && !contains(c.cfg.AllowedChats, chatID) {
		return false
	}
	return true
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
