package feishu_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/platform/feishu"
)

func TestRawPayloadPrefersRawBody(t *testing.T) {
	body := []byte(`{"header":{"event_type":"im.message.receive_v1"}}`)
	ev := &larkim.P2MessageReceiveV1{EventReq: &larkevent.EventReq{Body: body}}

	got, err := feishu.RawPayload(ev)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("raw payload = %s", got)
	}
}

func TestRawPayloadFallsBackToMarshal(t *testing.T) {
	got, err := feishu.RawPayload(&larkim.P2MessageReceiveV1{})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("回退序列化结果不是合法 JSON: %v", err)
	}
	if _, ok := decoded["event"]; !ok {
		t.Fatalf("缺少 event 字段: %s", got)
	}
}

func TestRawPayloadRejectsNil(t *testing.T) {
	if _, err := feishu.RawPayload(nil); err == nil {
		t.Fatal("nil 事件应返回错误")
	}
}

// TestDispatcherHandlesWebsocketPayload 验证长连接拿到的原始报文与 webhook 走同一条解析路径。
func TestDispatcherHandlesWebsocketPayload(t *testing.T) {
	api, srv := newFakeAPI(t)
	client := feishu.NewClient("cli_test", "secret", feishu.WithBaseURL(srv.URL))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := core.New(agent.Echo{}, feishu.NewChannel(client), logger)
	dispatcher := feishu.NewDispatcher(engine, feishu.Options{BotOpenID: "ou_bot"}, logger)

	body := []byte(messageJSON("ev_ws", "om_ws", "oc_ws", "p2p", "来自长连接", false))
	if err := dispatcher.HandlePayload(context.Background(), body); err != nil {
		t.Fatalf("handle payload: %v", err)
	}
	if err := engine.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	sent := api.messages()
	if len(sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(sent))
	}
	content, _ := sent[0].body["content"].(string)
	if !strings.Contains(content, "来自长连接") {
		t.Fatalf("content = %s", content)
	}
}

// fakeConnector 模拟事件来源，用于验证 Connector 契约与受理循环。
type fakeConnector struct {
	payloads [][]byte
	err      error
}

func (f *fakeConnector) Run(ctx context.Context, handle func(context.Context, []byte) error) error {
	for _, p := range f.payloads {
		if err := handle(ctx, p); err != nil {
			return err
		}
	}
	return f.err
}

func TestConnectorContract(t *testing.T) {
	api, srv := newFakeAPI(t)
	client := feishu.NewClient("cli_test", "secret", feishu.WithBaseURL(srv.URL))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := core.New(agent.Echo{}, feishu.NewChannel(client), logger)
	dispatcher := feishu.NewDispatcher(engine, feishu.Options{BotOpenID: "ou_bot"}, logger)

	conn := &fakeConnector{payloads: [][]byte{
		[]byte(messageJSON("ev_a", "om_a", "oc_a", "p2p", "第一条", false)),
		[]byte(messageJSON("ev_b", "om_b", "oc_b", "p2p", "第二条", false)),
	}}
	if err := conn.Run(context.Background(), dispatcher.HandlePayload); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(api.messages()); got != 2 {
		t.Fatalf("两个会话各应投递一次，实际 %d", got)
	}
}

func TestWSConnectorStartsDisconnected(t *testing.T) {
	c := feishu.NewWSConnector("cli", "secret", "", nil)
	if c.Ready() || c.Connected() {
		t.Fatal("未启动前不应报告已连接")
	}
}
