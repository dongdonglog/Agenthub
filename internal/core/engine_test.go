package core_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
)

type captureChannel struct {
	mu   sync.Mutex
	sent []channel.Outbound
	fail error
}

func (c *captureChannel) SendText(_ context.Context, out channel.Outbound) (channel.Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return channel.Receipt{}, c.fail
	}
	c.sent = append(c.sent, out)
	return channel.Receipt{ExternalID: "om_out"}, nil
}

func (c *captureChannel) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sent)
}

func newEngine(t *testing.T, ch channel.Channel, a agent.Agent) *core.Engine {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return core.New(a, ch, logger)
}

func inbound(id, text string) channel.Inbound {
	return channel.Inbound{
		Channel:    "feishu",
		Tenant:     "tenant_1",
		ExternalID: id,
		SentAt:     time.Unix(1700000000, 0),
		Sender:     channel.Sender{OpenID: "ou_user"},
		Chat:       channel.Chat{ID: "oc_chat", Kind: "p2p"},
		Text:       text,
		MsgType:    "text",
	}
}

func TestDuplicateMessageRunsOnce(t *testing.T) {
	ch := &captureChannel{}
	eng := newEngine(t, ch, agent.Echo{})
	ctx := context.Background()

	ok, err := eng.Submit(ctx, inbound("om_1", "你好"))
	if err != nil || !ok {
		t.Fatalf("submit: ok=%v err=%v", ok, err)
	}
	ok, err = eng.Submit(ctx, inbound("om_1", "你好"))
	if err != nil || ok {
		t.Fatalf("重复投递应被忽略: ok=%v err=%v", ok, err)
	}

	if err := eng.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := len(eng.Runs()); got != 1 {
		t.Fatalf("runs = %d, want 1", got)
	}
	if got := ch.count(); got != 1 {
		t.Fatalf("sent = %d, want 1", got)
	}
}

func TestRapidMessagesMergeIntoOneBatch(t *testing.T) {
	ch := &captureChannel{}
	eng := newEngine(t, ch, agent.Echo{})
	ctx := context.Background()

	for _, text := range []string{"机械臂停了", "没有报错", "你综合看一下"} {
		if _, err := eng.Submit(ctx, inbound("om_"+text, text)); err != nil {
			t.Fatal(err)
		}
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	runs := eng.Runs()
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if len(runs[0].InputIDs) != 3 {
		t.Fatalf("inputs = %d, want 3", len(runs[0].InputIDs))
	}
	if ch.count() != 1 {
		t.Fatalf("sent = %d, want 1", ch.count())
	}
	for _, m := range eng.Messages("tenant_1:oc_chat:ou_user") {
		if m.Status != "done" {
			t.Fatalf("message %s status = %s", m.ID, m.Status)
		}
	}
}

func TestFlushIsIdempotentForDelivery(t *testing.T) {
	ch := &captureChannel{}
	eng := newEngine(t, ch, agent.Echo{})
	ctx := context.Background()

	if _, err := eng.Submit(ctx, inbound("om_a", "hi")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := eng.Flush(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if ch.count() != 1 {
		t.Fatalf("重复 Flush 不应重复投递: sent = %d", ch.count())
	}
}

func TestDeliveryFailureIsRecorded(t *testing.T) {
	ch := &captureChannel{fail: errors.New("network down")}
	eng := newEngine(t, ch, agent.Echo{})
	ctx := context.Background()

	if _, err := eng.Submit(ctx, inbound("om_b", "hi")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	d := eng.Deliveries()
	if len(d) != 1 || d[0].State != "failed" {
		t.Fatalf("delivery = %+v", d)
	}
}

func TestAgentErrorBecomesFailedRun(t *testing.T) {
	ch := &captureChannel{}
	eng := newEngine(t, ch, failingAgent{})
	ctx := context.Background()

	if _, err := eng.Submit(ctx, inbound("om_c", "hi")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	runs := eng.Runs()
	if len(runs) != 1 || runs[0].State != "failed" {
		t.Fatalf("runs = %+v", runs)
	}
	if ch.count() != 1 {
		t.Fatal("失败也必须投递说明")
	}
}

func TestStopInterruptsRun(t *testing.T) {
	ch := &captureChannel{}
	blocking := &blockingAgent{started: make(chan struct{}), release: make(chan struct{})}
	eng := newEngine(t, ch, blocking)
	conv := "tenant_1:oc_chat:ou_user"

	if _, err := eng.Submit(context.Background(), inbound("om_d", "长任务")); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Flush(context.Background())
	}()

	select {
	case <-blocking.started:
	case <-time.After(2 * time.Second):
		t.Fatal("agent 未启动")
	}
	if !eng.Stop(conv) {
		t.Fatal("stop 应返回 true")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("停止后运行未结束")
	}

	runs := eng.Runs()
	if len(runs) != 1 || runs[0].State != "interrupted" {
		t.Fatalf("runs = %+v", runs)
	}
	if ch.count() != 1 {
		t.Fatal("中断也必须投递说明")
	}
}

type failingAgent struct{}

func (failingAgent) Run(context.Context, agent.Input) (agent.Output, error) {
	return agent.Output{}, errors.New("model unavailable")
}

type blockingAgent struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (a *blockingAgent) Run(ctx context.Context, _ agent.Input) (agent.Output, error) {
	a.once.Do(func() { close(a.started) })
	select {
	case <-ctx.Done():
		return agent.Output{}, ctx.Err()
	case <-a.release:
		return agent.Output{Text: "done"}, nil
	}
}
