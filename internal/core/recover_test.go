package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
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
