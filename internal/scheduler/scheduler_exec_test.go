package scheduler_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/scheduler"
	"github.com/moying/agenthub/internal/store"
)

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("AGENTHUB_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("未设置 AGENTHUB_TEST_DATABASE_URL，跳过真实数据库用例")
	}
	if u, err := url.Parse(raw); err == nil && strings.TrimPrefix(u.Path, "/") == "postgres" {
		t.Skip("测试库不能指向 postgres（会清空数据），请使用独立数据库")
	}
	return raw
}

type captureChannel struct{}

func (captureChannel) SendText(context.Context, channel.Outbound) (channel.Receipt, error) {
	return channel.Receipt{}, nil
}

// newHarness 准备一个干净的调度环境。
func newHarness(t *testing.T) (*store.Journal, *core.Engine, *store.DB, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, testDatabaseURL(t), 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, db, "../../migrations"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `DELETE FROM scheduled_task WHERE id LIKE 'test_%'`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `TRUNCATE delivery_state, run_artifact, run_trace, message_state, run_state`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	journal := store.NewJournal(db)
	engine := core.New(agent.Echo{}, captureChannel{}, nil)
	engine.SetJournal(journal)
	return journal, engine, db, func() { db.Close() }
}

func saveTask(t *testing.T, j *store.Journal, id string, enabled bool, next *time.Time) {
	t.Helper()
	if err := j.SaveTask(context.Background(), store.ScheduledTask{
		ID: id, Name: id, Cron: "* * * * *", Timezone: "Asia/Shanghai",
		ChatID: "chat", Channel: "feishu", Prompt: "巡检一下", Enabled: enabled, NextRunAt: next,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestTickExecutesDueTask 覆盖调度器唯一的执行路径。
//
// @remarks 到期任务必须被领取、提交到引擎、回填 last_run_at 与下次时间；
// 这条链路此前没有任何测试，改坏了不会有人发现。
func TestTickExecutesDueTask(t *testing.T) {
	journal, engine, _, cleanup := newHarness(t)
	defer cleanup()

	past := time.Now().Add(-time.Minute)
	saveTask(t, journal, "test_due", true, &past)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := scheduler.New(journal, engine, nil)
	go s.Run(ctx)

	// Run 首次循环即执行 tick；等任务回填完成再断言。
	deadline := time.Now().Add(3 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		task, err := journal.GetTask(context.Background(), "test_due")
		if err == nil && task.LastStatus != "" {
			status = task.LastStatus
			if task.NextRunAt == nil || task.LastRunAt == nil {
				t.Fatalf("执行后应回填 next_run_at 与 last_run_at，实际 next=%v last=%v", task.NextRunAt, task.LastRunAt)
			}
			if !task.NextRunAt.After(time.Now()) {
				t.Fatalf("下次运行时间应在将来，实际 %s", task.NextRunAt)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != "ok" {
		t.Fatalf("last_status = %q, want ok（任务未被执行或提交失败）", status)
	}

	// 任务确实进入引擎：会话键为 cron:<chat>:scheduler。
	msgs := engine.Messages("cron:chat:scheduler")
	if len(msgs) != 1 {
		t.Fatalf("引擎内消息数 = %d, want 1", len(msgs))
	}
	if msgs[0].Text != "巡检一下" {
		t.Fatalf("消息内容 = %q", msgs[0].Text)
	}
	if !msgs[0].NoReply {
		t.Fatal("定时任务消息应标记为 NoReply")
	}
}

// TestTickSkipsDisabledTask 确认停用任务不会被领取。
func TestTickSkipsDisabledTask(t *testing.T) {
	journal, engine, _, cleanup := newHarness(t)
	defer cleanup()

	past := time.Now().Add(-time.Minute)
	saveTask(t, journal, "test_disabled", false, &past)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go scheduler.New(journal, engine, nil).Run(ctx)

	time.Sleep(300 * time.Millisecond)
	task, err := journal.GetTask(context.Background(), "test_disabled")
	if err != nil {
		t.Fatal(err)
	}
	if task.LastStatus != "" {
		t.Fatalf("停用任务不应执行，last_status = %q", task.LastStatus)
	}
	if len(engine.Messages("cron:chat:scheduler")) != 0 {
		t.Fatal("停用任务不应提交到引擎")
	}
}

// TestTickRecoversStaleClaim 覆盖领取后崩溃的兜底。
//
// @remarks 任务被领取（claimed_at 已写、next_run_at 置空）后进程崩溃，
// 必须由 tick 找回并重算下次时间，否则任务永久停摆且不报错。
func TestTickRecoversStaleClaim(t *testing.T) {
	journal, engine, db, cleanup := newHarness(t)
	defer cleanup()
	ctx := context.Background()

	// 造出「已领取但未回填」的状态：ClaimDue 会把 next_run_at 置空并写 claimed_at，
	// 再把 claimed_at 回退到超过 ClaimStaleAfter 之前，模拟领取后进程崩溃。
	past := time.Now().Add(-time.Minute)
	saveTask(t, journal, "test_stale", true, &past)
	claimed, err := journal.ClaimDue(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != "test_stale" {
		t.Fatalf("预期领取 test_stale，实际 %+v", claimed)
	}
	if _, err := db.Pool().Exec(ctx,
		`UPDATE scheduled_task SET claimed_at = $2 WHERE id = $1`,
		"test_stale", time.Now().Add(-2*store.ClaimStaleAfter)); err != nil {
		t.Fatal(err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go scheduler.New(journal, engine, nil).Run(runCtx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, err := journal.GetTask(ctx, "test_stale")
		if err == nil && task.NextRunAt != nil {
			if task.LastRunAt != nil {
				t.Fatal("僵尸领取被找回时不应写 last_run_at（任务并未真正执行）")
			}
			if !strings.Contains(task.LastStatus, "recovered") {
				t.Fatalf("last_status = %q, want recovered 说明", task.LastStatus)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("超时未找回僵尸领取")
}

// TestRunNowDoesNotShiftSchedule 确认「立即运行」不动既定的下次时间。
func TestRunNowDoesNotShiftSchedule(t *testing.T) {
	journal, engine, _, cleanup := newHarness(t)
	defer cleanup()

	planned := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	saveTask(t, journal, "test_now", true, &planned)

	task, err := journal.GetTask(context.Background(), "test_now")
	if err != nil {
		t.Fatal(err)
	}
	s := scheduler.New(journal, engine, nil)
	if err := s.RunNow(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	after, err := journal.GetTask(context.Background(), "test_now")
	if err != nil {
		t.Fatal(err)
	}
	if after.LastStatus != "ok" {
		t.Fatalf("last_status = %q, want ok", after.LastStatus)
	}
	if after.NextRunAt == nil || !after.NextRunAt.Equal(planned) {
		t.Fatalf("立即运行不应改动下次时间：got %v want %v", after.NextRunAt, planned)
	}
	if len(engine.Messages("cron:chat:scheduler")) != 1 {
		t.Fatal("立即运行应提交一次")
	}
}
