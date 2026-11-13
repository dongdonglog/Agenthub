package core_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
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
	eng.SetRetryDelay(func(int) time.Duration { return 5 * time.Millisecond })
	ctx := context.Background()

	if _, err := eng.Submit(ctx, inbound("om_b", "hi")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		d := eng.Deliveries()
		if len(d) == 1 && d[0].State == "failed" {
			if d[0].Attempts < 2 {
				t.Fatalf("重试次数不足: %+v", d[0])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("投递应在重试后进入 failed: %+v", eng.Deliveries())
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

type fakePresenter struct {
	opened   int
	updated  int
	closed   int
	body     string
	closeErr error
}

func (f *fakePresenter) Open(context.Context, core.Presentation) (string, error) {
	f.opened++
	return "card_1", nil
}

func (f *fakePresenter) Update(context.Context, string, string, string) error {
	f.updated++
	return nil
}

func (f *fakePresenter) Close(_ context.Context, _ string, body string, _ bool) error {
	f.closed++
	f.body = body
	return f.closeErr
}

func TestPresenterCarriesResultInsteadOfText(t *testing.T) {
	ch := &captureChannel{}
	eng := newEngine(t, ch, agent.Echo{})
	presenter := &fakePresenter{}
	eng.SetPresenter(presenter)

	if _, err := eng.Submit(context.Background(), inbound("om_p", "卡片")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	if presenter.opened != 1 || presenter.closed != 1 {
		t.Fatalf("presenter opened=%d closed=%d", presenter.opened, presenter.closed)
	}
	if ch.count() != 0 {
		t.Fatalf("卡片成功后不应再发文本，实际 %d", ch.count())
	}
	if !strings.Contains(presenter.body, "卡片") {
		t.Fatalf("卡片正文 = %q", presenter.body)
	}
	d := eng.Deliveries()
	if len(d) != 1 || d[0].State != "sent" {
		t.Fatalf("delivery = %+v", d)
	}
}

func TestPresenterFailureFallsBackToText(t *testing.T) {
	ch := &captureChannel{}
	eng := newEngine(t, ch, agent.Echo{})
	eng.SetPresenter(&fakePresenter{closeErr: errors.New("card failed")})

	if _, err := eng.Submit(context.Background(), inbound("om_pf", "回退")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ch.count() != 1 {
		t.Fatalf("卡片失败应回退文本，实际 %d", ch.count())
	}
}

type recordingAgent struct {
	history []agent.Message
}

func (r *recordingAgent) Run(_ context.Context, in agent.Input) (agent.Output, error) {
	r.history = append([]agent.Message(nil), in.History...)
	return agent.Output{Text: "回答：" + in.Messages[0].Text}, nil
}

func TestSecondTurnCarriesHistory(t *testing.T) {
	ch := &captureChannel{}
	recorder := &recordingAgent{}
	eng := newEngine(t, ch, recorder)
	ctx := context.Background()

	if _, err := eng.Submit(ctx, inbound("om_t1", "第一问")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Submit(ctx, inbound("om_t2", "第二问")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	if len(recorder.history) < 2 {
		t.Fatalf("第二轮应带上历史，实际 %d 条", len(recorder.history))
	}
	if recorder.history[0].Role != "user" || recorder.history[0].Text != "第一问" {
		t.Fatalf("历史第一条 = %+v", recorder.history[0])
	}
	if recorder.history[1].Role != "assistant" {
		t.Fatalf("历史应包含助手回复，实际 %+v", recorder.history[1])
	}
}

func TestResetStartsNewGeneration(t *testing.T) {
	ch := &captureChannel{}
	recorder := &recordingAgent{}
	eng := newEngine(t, ch, recorder)
	ctx := context.Background()

	if _, err := eng.Submit(ctx, inbound("om_g1", "旧话题")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	eng.Reset("tenant_1:oc_chat:ou_user")
	if _, err := eng.Submit(ctx, inbound("om_g2", "新话题")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(recorder.history) != 0 {
		t.Fatalf("reset 后不应带旧历史，实际 %d 条", len(recorder.history))
	}
	if gen := eng.Generation("tenant_1:oc_chat:ou_user"); gen != 2 {
		t.Fatalf("generation = %d, want 2", gen)
	}
}

type flakyChannel struct {
	fails int
	sent  int
	mu    sync.Mutex
}

func (c *flakyChannel) SendText(context.Context, channel.Outbound) (channel.Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fails > 0 {
		c.fails--
		return channel.Receipt{}, errors.New("temporary failure")
	}
	c.sent++
	return channel.Receipt{ExternalID: "om_ok"}, nil
}

func TestDeliveryRetriesThenSucceeds(t *testing.T) {
	ch := &flakyChannel{fails: 2}
	eng := newEngine(t, ch, agent.Echo{})
	eng.SetRetryDelay(func(int) time.Duration { return 10 * time.Millisecond })

	if _, err := eng.Submit(context.Background(), inbound("om_retry", "重试")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ch.mu.Lock()
		sent := ch.sent
		ch.mu.Unlock()
		if sent == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.sent != 1 {
		t.Fatalf("重试后应成功投递一次，实际 %d", ch.sent)
	}
}

type toolAgent struct{}

func (toolAgent) Run(_ context.Context, in agent.Input) (agent.Output, error) {
	if in.Progress != nil {
		in.Progress("正在分析")
	}
	if in.OnTool != nil {
		in.OnTool("mcp_demo_get_time", 5*time.Millisecond, nil)
	}
	return agent.Output{Text: "done"}, nil
}

func TestRunEventsRecorded(t *testing.T) {
	ch := &captureChannel{}
	eng := newEngine(t, ch, toolAgent{})
	ctx := context.Background()
	if _, err := eng.Submit(ctx, inbound("om_evt", "hi")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	events := eng.RunEvents("run_1")
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Kind != "phase" || events[1].Kind != "tool" {
		t.Fatalf("events = %+v", events)
	}
	if events[1].DurationMS < 0 || events[1].Name != "mcp_demo_get_time" {
		t.Fatalf("tool event = %+v", events[1])
	}
}

type usageAgent struct{}

func (usageAgent) Run(context.Context, agent.Input) (agent.Output, error) {
	return agent.Output{Text: "ok", Usage: agent.Usage{InputTokens: 11, OutputTokens: 4}}, nil
}

func TestRunUsageRecorded(t *testing.T) {
	ch := &captureChannel{}
	eng := newEngine(t, ch, usageAgent{})
	ctx := context.Background()
	if _, err := eng.Submit(ctx, inbound("om_usage", "hi")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	runs := eng.Runs()
	if len(runs) != 1 || runs[0].InputTokens != 11 || runs[0].OutputTokens != 4 {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestPruneBoundsMemory(t *testing.T) {
	ch := &captureChannel{}
	eng := newEngine(t, ch, agent.Echo{})
	eng.SetLimits(4, 1, 2, 2)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := eng.Submit(ctx, inbound(fmt.Sprintf("om_p%d", i), "消息")); err != nil {
			t.Fatal(err)
		}
		if err := eng.Flush(ctx); err != nil {
			t.Fatal(err)
		}
	}
	eng.Prune()
	if got := len(eng.Messages("tenant_1:oc_chat:ou_user")); got > 4 {
		t.Fatalf("消息数未裁剪: %d", got)
	}

	// 新会话写入后，旧会话应被淘汰（会话上限 1）。
	if _, err := eng.Submit(ctx, channel.Inbound{
		Channel: "feishu", Tenant: "t2", ExternalID: "new-1",
		Chat:   channel.Chat{ID: "chat-b", Kind: "p2p"},
		Sender: channel.Sender{OpenID: "u2"}, Text: "新会话",
	}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	eng.Prune()
	if got := len(eng.Conversations()); got != 1 {
		t.Fatalf("会话数未裁剪: %d", got)
	}
}

func TestWaitIdle(t *testing.T) {
	eng := newEngine(t, &captureChannel{}, &blockingAgent{started: make(chan struct{}), release: make(chan struct{})})
	// blockingAgent in this package uses started/release channels.
	conv := "tenant_1:oc_chat:ou_user"
	if _, err := eng.Submit(context.Background(), inbound("om_wait", "长任务")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = eng.Flush(context.Background()) }()
	time.Sleep(50 * time.Millisecond)

	short, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if eng.WaitIdle(short) {
		t.Fatal("运行未结束时不应空闲")
	}

	eng.Stop(conv)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("停止后运行未结束")
	}
	idle, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if !eng.WaitIdle(idle) {
		t.Fatal("运行结束后应空闲")
	}
}

func TestTraceIDPropagates(t *testing.T) {
	ch := &captureChannel{}
	eng := newEngine(t, ch, agent.Echo{})
	ctx := context.Background()
	if _, err := eng.Submit(ctx, inbound("om_trace", "hi")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	msgs := eng.Messages("tenant_1:oc_chat:ou_user")
	if len(msgs) == 0 || msgs[0].TraceID == "" {
		t.Fatalf("消息缺少 trace: %+v", msgs)
	}
	runs := eng.Runs()
	if len(runs) != 1 || runs[0].TraceID != msgs[0].TraceID {
		t.Fatalf("运行 trace 不一致: %+v", runs)
	}
	d := eng.Deliveries()
	if len(d) != 1 || d[0].TraceID != runs[0].TraceID {
		t.Fatalf("投递 trace 不一致: %+v", d)
	}
}

type profileAgent struct {
	last agent.Input
}

func (p *profileAgent) Run(_ context.Context, in agent.Input) (agent.Output, error) {
	p.last = in
	return agent.Output{Text: "ok"}, nil
}

func TestProfileBindingSticksPerGeneration(t *testing.T) {
	pa := &profileAgent{}
	eng := newEngine(t, &captureChannel{}, pa)
	ctx := context.Background()

	eng.SetProfileSource(func() core.ProfileSelection {
		return core.ProfileSelection{Name: "ops", Skills: []string{"a"}, MCPs: []string{"demo"}}
	})
	if _, err := eng.Submit(ctx, inbound("om_p1", "第一问")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(pa.last.SkillAllow) != 1 || pa.last.SkillAllow[0] != "a" || len(pa.last.MCPAllow) != 1 {
		t.Fatalf("首轮绑定 = %+v", pa.last)
	}

	// 切换全局 Profile，当前代不应受影响。
	eng.SetProfileSource(func() core.ProfileSelection {
		return core.ProfileSelection{Name: "dev", Skills: []string{"b"}}
	})
	if _, err := eng.Submit(ctx, inbound("om_p2", "第二问")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if pa.last.SkillAllow[0] != "a" {
		t.Fatalf("同一代不应切换: %+v", pa.last)
	}

	// /reset 后新代采用新的全局 Profile。
	eng.Reset("tenant_1:oc_chat:ou_user")
	if _, err := eng.Submit(ctx, inbound("om_p3", "新代")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if pa.last.SkillAllow[0] != "b" {
		t.Fatalf("新代应使用新 Profile: %+v", pa.last)
	}
	if got := eng.ProfileOf("tenant_1:oc_chat:ou_user"); got != "dev" {
		t.Fatalf("ProfileOf = %s", got)
	}
}

func TestSessionInfosDisplayName(t *testing.T) {
	eng := newEngine(t, &captureChannel{}, agent.Echo{})
	ctx := context.Background()
	if _, err := eng.Submit(ctx, inbound("om_id", "hi")); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	conv := "tenant_1:oc_chat:ou_user"
	eng.SetIdentity(conv, "运维群", "张三")
	infos := eng.SessionInfos()
	if len(infos) != 1 {
		t.Fatalf("infos = %+v", infos)
	}
	if infos[0].DisplayName != "群聊 · 运维群 · 张三" || infos[0].Messages != 2 {
		t.Fatalf("info = %+v", infos[0])
	}
}

type concurrentAgent struct {
	started chan string
	release chan struct{}
}

func (a *concurrentAgent) Run(ctx context.Context, in agent.Input) (agent.Output, error) {
	a.started <- in.ConversationID
	select {
	case <-a.release:
	case <-ctx.Done():
		return agent.Output{}, ctx.Err()
	}
	return agent.Output{Text: "ok"}, nil
}

func TestDispatchRunsConversationsConcurrently(t *testing.T) {
	ca := &concurrentAgent{started: make(chan string, 4), release: make(chan struct{})}
	eng := newEngine(t, &captureChannel{}, ca)
	eng.SetConcurrency(2)
	ctx := context.Background()

	for i, conv := range []string{"a", "b"} {
		if _, err := eng.Submit(ctx, channel.Inbound{
			Channel: "feishu", Tenant: "t", ExternalID: "e" + conv,
			Chat:   channel.Chat{ID: "chat-" + conv, Kind: "p2p"},
			Sender: channel.Sender{OpenID: "u" + conv}, Text: "hi" + conv,
		}); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	eng.Dispatch(ctx)
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case conv := <-ca.started:
			seen[conv] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("并发未生效，已启动: %v", seen)
		}
	}
	close(ca.release)
	idle, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !eng.WaitIdle(idle) {
		t.Fatal("运行未结束")
	}
}

func TestRateLimitPerConversation(t *testing.T) {
	eng := newEngine(t, &captureChannel{}, agent.Echo{})
	eng.SetRateLimit(1, time.Minute)
	ctx := context.Background()
	in := inbound("om_rate_1", "hi")
	if _, err := eng.Submit(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.ExternalID = "om_rate_2"
	if _, err := eng.Submit(ctx, in); !errors.Is(err, core.ErrRateLimited) {
		t.Fatalf("第二次应限流，err = %v", err)
	}
	// 另一个会话不受影响。
	other := channel.Inbound{
		Channel: "feishu", Tenant: "t", ExternalID: "om_rate_3",
		Chat:   channel.Chat{ID: "chat-other", Kind: "p2p"},
		Sender: channel.Sender{OpenID: "u-other"}, Text: "hi",
	}
	if _, err := eng.Submit(ctx, other); err != nil {
		t.Fatalf("其他会话不应被限流: %v", err)
	}
}

func TestDispatchPriorityAndFIFO(t *testing.T) {
	ca := &concurrentAgent{started: make(chan string, 8), release: make(chan struct{})}
	eng := newEngine(t, &captureChannel{}, ca)
	eng.SetConcurrency(1)
	ctx := context.Background()

	submit := func(tenant, chat, user, kind, ext string) {
		t.Helper()
		if _, err := eng.Submit(ctx, channel.Inbound{
			Channel: "feishu", Tenant: tenant, ExternalID: ext,
			Chat:   channel.Chat{ID: chat, Kind: kind},
			Sender: channel.Sender{OpenID: user}, Text: "hi",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 先来一个群聊（优先级 5），再来一个私聊（优先级 10）。
	submit("t", "chat-group", "u1", "group", "g1")
	submit("t", "chat-p2p", "u2", "p2p", "p1")
	eng.Dispatch(ctx)
	select {
	case conv := <-ca.started:
		if !strings.Contains(conv, "chat-p2p") {
			t.Fatalf("应优先调度私聊，实际 %s", conv)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("没有运行启动")
	}
	close(ca.release)
	idle, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !eng.WaitIdle(idle) {
		t.Fatal("运行未结束")
	}
}

func TestDispatchAgingPreventsStarvation(t *testing.T) {
	ca := &concurrentAgent{started: make(chan string, 8), release: make(chan struct{})}
	eng := newEngine(t, &captureChannel{}, ca)
	eng.SetConcurrency(1)
	ctx := context.Background()
	base := time.Now()
	eng.SetNow(func() time.Time { return base })

	if _, err := eng.Submit(ctx, channel.Inbound{
		Channel: "feishu", Tenant: "t", ExternalID: "old-group",
		Chat:   channel.Chat{ID: "old-group", Kind: "group"},
		Sender: channel.Sender{OpenID: "u1"}, Text: "排队很久的群消息",
	}); err != nil {
		t.Fatal(err)
	}

	// 10 分钟后来了一个私聊：老化让旧群消息的有效优先级更高。
	eng.SetNow(func() time.Time { return base.Add(10 * time.Minute) })
	if _, err := eng.Submit(ctx, channel.Inbound{
		Channel: "feishu", Tenant: "t", ExternalID: "new-p2p",
		Chat:   channel.Chat{ID: "new-p2p", Kind: "p2p"},
		Sender: channel.Sender{OpenID: "u2"}, Text: "新私聊",
	}); err != nil {
		t.Fatal(err)
	}

	items := eng.QueueSnapshot()
	if len(items) != 2 || items[0].ConversationID != "t:old-group:u1" {
		t.Fatalf("老化后应优先旧群消息: %+v", items)
	}
	eng.Dispatch(ctx)
	select {
	case conv := <-ca.started:
		if conv != "t:old-group:u1" {
			t.Fatalf("应调度等待更久的会话，实际 %s", conv)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("没有运行启动")
	}
	close(ca.release)
	idle, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !eng.WaitIdle(idle) {
		t.Fatal("运行未结束")
	}
}

func TestScheduledMessageDoesNotReplyToExternalID(t *testing.T) {
	ch := &captureChannel{}
	eng := newEngine(t, ch, agent.Echo{})
	ctx := context.Background()
	if _, err := eng.Submit(ctx, channel.Inbound{
		Channel: "cron", Tenant: "cron", ExternalID: "cron_task_1_2",
		NoReply: true,
		Chat:    channel.Chat{ID: "oc_test", Kind: "group"},
		Sender:  channel.Sender{OpenID: "scheduler"},
		Text:    "定时任务自检",
	}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if len(ch.sent) == 0 {
		t.Fatal("没有投递")
	}
	if ch.sent[0].ReplyTo != "" {
		t.Fatalf("定时任务不应回复消息，ReplyTo = %q", ch.sent[0].ReplyTo)
	}
	if ch.sent[0].ChatID != "oc_test" {
		t.Fatalf("ChatID = %s", ch.sent[0].ChatID)
	}
}

func TestResetCleansOldGeneration(t *testing.T) {
	eng := newEngine(t, &captureChannel{}, agent.Echo{})
	cleaned := make(chan int, 1)
	eng.SetGenerationCleaner(func(_ string, generation int) { cleaned <- generation })
	eng.Reset("tenant_1:oc_chat:ou_user")
	select {
	case generation := <-cleaned:
		if generation != 1 {
			t.Fatalf("cleaned generation = %d", generation)
		}
	case <-time.After(time.Second):
		t.Fatal("未调用清理回调")
	}
}
