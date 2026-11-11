package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/larksuite/oapi-sdk-go/v3/ws"
)

// Connector 是事件来源抽象。
//
// @remarks 长连接与回调共用同一份受理逻辑；实现只负责把原始事件 JSON
// 交给 handle。Run 应阻塞到 ctx 结束或不可恢复的错误。
type Connector interface {
	Run(ctx context.Context, handle func(context.Context, []byte) error) error
}

// WSConnector 通过飞书 WebSocket 长连接接收事件。
//
// @remarks 长连接不需要公网入口与回调地址；SDK 负责握手、心跳与自动重连。
// 集群模式下同一应用只有一个客户端会收到事件，因此单实例部署是 v1 的默认形态。
type WSConnector struct {
	appID     string
	appSecret string
	domain    string
	log       *slog.Logger
	ready     atomic.Bool
	connected atomic.Bool
}

// NewWSConnector 构造长连接事件源。
//
// @param domain 开放平台地址；为空时使用飞书默认域名
func NewWSConnector(appID, appSecret, domain string, log *slog.Logger) *WSConnector {
	if log == nil {
		log = slog.Default()
	}
	return &WSConnector{appID: appID, appSecret: appSecret, domain: domain, log: log}
}

// Ready 返回是否已完成首次握手。
func (c *WSConnector) Ready() bool { return c.ready.Load() }

// Connected 返回当前连接是否可用。
func (c *WSConnector) Connected() bool { return c.connected.Load() }

// Run 建立长连接并分发事件，直到 ctx 结束。
func (c *WSConnector) Run(ctx context.Context, handle func(context.Context, []byte) error) error {
	dispatcher := larkdispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			raw, err := RawPayload(event)
			if err != nil {
				return err
			}
			return handle(ctx, raw)
		})

	opts := []ws.ClientOption{
		ws.WithEventHandler(dispatcher),
		ws.WithLogLevel(larkcore.LogLevelError),
	}
	if c.domain != "" {
		opts = append(opts, ws.WithDomain(c.domain))
	}
	client := ws.NewClient(c.appID, c.appSecret, opts...)

	client.SetOnReady(func() {
		c.ready.Store(true)
		c.connected.Store(true)
		c.log.Info("feishu websocket ready")
	})
	client.SetOnReconnecting(func() {
		c.connected.Store(false)
		c.log.Warn("feishu websocket reconnecting")
	})
	client.SetOnReconnected(func() {
		c.connected.Store(true)
		c.log.Info("feishu websocket reconnected")
	})
	client.SetOnDisconnected(func() {
		c.connected.Store(false)
		c.log.Warn("feishu websocket disconnected")
	})
	client.SetOnError(func(err error) {
		c.log.Error("feishu websocket error", "error", err)
	})

	err := client.Start(ctx)
	if ctx.Err() != nil {
		// 正常退出：由调用方关闭流程。
		return nil
	}
	return err
}

// RawPayload 取出事件的原始 JSON。
//
// @remarks 优先使用回调携带的原始报文，保证与 webhook 走完全相同的解析路径；
// 缺失时回退为序列化 SDK 事件。
func RawPayload(event *larkim.P2MessageReceiveV1) ([]byte, error) {
	if event == nil {
		return nil, fmt.Errorf("空事件")
	}
	if event.EventReq != nil && len(event.EventReq.Body) > 0 {
		return event.EventReq.Body, nil
	}
	return json.Marshal(event)
}
