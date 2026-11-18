package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

// TestRecoverStaleRuns 覆盖进程被 kill 后的场景：库里留着 state=running 的运行，
// 没有任何执行者。不回收，卡片会永远停在「处理中」。
func TestRecoverStaleRuns(t *testing.T) {
	url := testDatabaseURL(t)
	ctx := context.Background()
	db, err := store.Open(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `TRUNCATE message_state, run_state, delivery_state`); err != nil {
		t.Fatal(err)
	}

	journal := store.NewJournal(db)
	now := time.Now()
	stale := &core.Run{
		ID: "run_stale", ConversationID: "aiops:inc_x", ChatID: "oc_chat",
		State: "running", InputIDs: []string{"msg_stale"},
		StartedAt: now.Add(-2 * time.Hour), TraceID: "tr_stale",
	}
	fresh := &core.Run{
		ID: "run_fresh", ConversationID: "aiops:inc_y", ChatID: "oc_chat",
		State: "running", InputIDs: []string{"msg_fresh"},
		StartedAt: now.Add(-time.Minute), TraceID: "tr_fresh",
	}
	done := &core.Run{
		ID: "run_done", ConversationID: "aiops:inc_z", ChatID: "oc_chat",
		State: "completed", InputIDs: []string{"msg_done"}, StartedAt: now.Add(-3 * time.Hour),
	}
	for _, r := range []*core.Run{stale, fresh, done} {
		if err := journal.SaveRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	ch := &captureChannel{}
	engine := core.New(agent.Echo{}, ch, nil)
	engine.SetJournal(journal)
	engine.SetStaleRunAfter(time.Hour)

	if n := engine.RecoverStaleRuns(ctx); n != 1 {
		t.Fatalf("回收数 = %d, want 1（只回收超时的 running）", n)
	}

	// 超时的被标记为 interrupted，且写入结束时间与原因。
	_, runs, err := journal.LoadRecent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*core.Run{}
	for _, r := range runs {
		byID[r.ID] = r
	}
	if got := byID["run_stale"]; got == nil || got.State != "interrupted" || got.EndedAt.IsZero() {
		t.Fatalf("run_stale = %+v", got)
	}
	if got := byID["run_fresh"]; got == nil || got.State != "running" {
		t.Fatalf("新鲜运行不应被回收: %+v", got)
	}
	if got := byID["run_done"]; got == nil || got.State != "completed" {
		t.Fatalf("终态运行不应被改写: %+v", got)
	}

	// 中断必须有结果说明（不变量：中断/失败都要有说明投递）。
	if ch.count() != 1 {
		t.Fatalf("应投递 1 条中断说明，实际 %d", ch.count())
	}

	// 重复执行不应重复投递。
	if n := engine.RecoverStaleRuns(ctx); n != 0 {
		t.Fatalf("第二次回收数 = %d, want 0", n)
	}
	if ch.count() != 1 {
		t.Fatalf("不应重复投递，实际 %d", ch.count())
	}
}

// TestRecoverStaleRunsInConversationWithLiveRun 覆盖真实踩到的场景：
// 重启后消息被重新排队，同一会话里同时存在「已死的旧运行」和「刚启动的新运行」。
//
// @remarks 旧实现按会话排除，旧运行永远回收不掉，卡片永久停在「处理中」。
// 这里用真实运行路径构造该场景，锁住两件事：
// 旧运行必须被回收；新运行的输入不能被旧运行的回收误标成 stopped。
func TestRecoverStaleRunsInConversationWithLiveRun(t *testing.T) {
	url := testDatabaseURL(t)
	ctx := context.Background()
	db, err := store.Open(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `TRUNCATE message_state, run_state, delivery_state`); err != nil {
		t.Fatal(err)
	}

	journal := store.NewJournal(db)
	presenter := &fakePresenter{}
	blocked := &blockingAgent{started: make(chan struct{}), release: make(chan struct{})}
	engine := core.New(blocked, &captureChannel{}, nil)
	engine.SetJournal(journal)
	engine.SetPresenter(presenter)
	engine.SetStaleRunAfter(time.Minute)

	conv := "aiops:inc_shared"
	if _, err := engine.Submit(ctx, channel.Inbound{
		Channel: "aiops", Tenant: "aiops", ConversationKey: conv,
		ExternalID: "incident:inc_shared:firing",
		Chat:       channel.Chat{ID: "oc_chat", Kind: "group"},
		Sender:     channel.Sender{OpenID: "alertmanager"},
		Text:       "诊断", MsgType: "text", NoReply: true,
	}); err != nil {
		t.Fatal(err)
	}
	flushDone := make(chan struct{})
	go func() { defer close(flushDone); _ = engine.Flush(ctx) }()
	select {
	case <-blocked.started:
	case <-time.After(2 * time.Second):
		t.Fatal("新运行未启动")
	}

	runs := engine.Runs()
	if len(runs) != 1 {
		t.Fatalf("运行数 = %d, want 1", len(runs))
	}
	liveID := runs[0].ID
	sharedInput := runs[0].InputIDs[0]

	// 插入一条同会话、同输入、心跳早已停止的旧运行，卡片还挂着。
	now := time.Now()
	dead := &core.Run{
		ID: "run_dead", ConversationID: conv, ChatID: "oc_chat",
		State: "running", InputIDs: []string{sharedInput},
		StartedAt: now.Add(-10 * time.Minute), HeartbeatAt: now.Add(-10 * time.Minute),
		CardHandle: "om_dead_card", TraceID: "tr_dead",
	}
	if err := journal.SaveRun(ctx, dead); err != nil {
		t.Fatal(err)
	}

	if n := engine.RecoverStaleRuns(ctx); n != 1 {
		t.Fatalf("回收数 = %d, want 1（旧运行必须被回收）", n)
	}

	_, loaded, err := journal.LoadRecent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*core.Run{}
	for _, r := range loaded {
		byID[r.ID] = r
	}
	if got := byID["run_dead"]; got == nil || got.State != "interrupted" {
		t.Fatalf("旧运行应被回收: %+v", got)
	}
	if got := byID[liveID]; got == nil || got.State != "running" {
		t.Fatalf("新运行不应被回收: %+v", got)
	}

	// 旧卡片被关掉。
	if !strings.Contains(presenter.body, "中断") {
		t.Fatalf("旧卡片应写入中断说明，实际 %q", presenter.body)
	}

	// 关键：与仍在执行的新运行共享的输入不能被标成 stopped。
	var status string
	if err := db.Pool().QueryRow(ctx, `SELECT status FROM message_state WHERE id = $1`, sharedInput).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == "stopped" {
		t.Fatal("共享输入被旧运行的回收误标为 stopped，仍在执行的新运行会被弄坏")
	}
	// 新运行仍可被停止：active 不能被旧运行的回收清掉。
	if !engine.Stop(conv) {
		t.Fatal("新运行的 active 被误清，已无法停止")
	}

	close(blocked.release)
	select {
	case <-flushDone:
	case <-time.After(3 * time.Second):
		t.Fatal("收尾超时")
	}
}

// TestSweepUnclosedCards 覆盖「运行已结束，但改卡片那步失败」这条路径。
//
// @remarks 只回收 running 是不够的：运行正常结束后如果 Close 卡片失败，
// 卡片同样会停在「处理中」。巡检必须持续重试直到成功。
func TestSweepUnclosedCards(t *testing.T) {
	url := testDatabaseURL(t)
	ctx := context.Background()
	db, err := store.Open(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `TRUNCATE message_state, run_state, delivery_state`); err != nil {
		t.Fatal(err)
	}

	journal := store.NewJournal(db)
	now := time.Now()
	run := &core.Run{
		ID: "run_card", ConversationID: "aiops:inc_card", ChatID: "oc_chat",
		State: "completed", Output: "诊断完成", StartedAt: now.Add(-time.Minute),
		EndedAt: now, TraceID: "tr_card", CardHandle: "card_open_1",
	}
	if err := journal.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	presenter := &fakePresenter{}
	engine := core.New(agent.Echo{}, &captureChannel{}, nil)
	engine.SetJournal(journal)
	engine.SetPresenter(presenter)

	// 第一次结算失败：卡片仍是「处理中」，且未被标记为已关闭。
	presenter.closeErr = errors.New("feishu 500")
	if n := engine.SweepUnclosedCards(ctx); n != 0 {
		t.Fatalf("失败时收尾数 = %d, want 0", n)
	}
	if pending, err := journal.LoadUnclosedCards(ctx, 10); err != nil || len(pending) != 1 {
		t.Fatalf("失败的卡片应留在待收尾列表: %v %v", pending, err)
	}

	// 恢复后重试成功：卡片写入终态，并从待收尾列表消失。
	presenter.closeErr = nil
	if n := engine.SweepUnclosedCards(ctx); n != 1 {
		t.Fatalf("重试收尾数 = %d, want 1", n)
	}
	if pending, err := journal.LoadUnclosedCards(ctx, 10); err != nil || len(pending) != 0 {
		t.Fatalf("收尾后不应再有待处理卡片: %v %v", pending, err)
	}
	if !strings.Contains(presenter.body, "诊断完成") {
		t.Fatalf("卡片应写入运行输出，实际 body = %q", presenter.body)
	}
}
