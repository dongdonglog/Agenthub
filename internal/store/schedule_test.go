package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/store"
)

func TestClaimDueAndFinish(t *testing.T) {
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
	if _, err := db.Pool().Exec(ctx, `DELETE FROM scheduled_task WHERE id LIKE 'test_%'`); err != nil {
		t.Fatal(err)
	}

	j := store.NewJournal(db)
	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Hour)
	for _, tc := range []struct {
		id   string
		next time.Time
	}{
		{"test_due", past},
		{"test_future", future},
	} {
		if err := j.SaveTask(ctx, store.ScheduledTask{
			ID: tc.id, Name: tc.id, Cron: "* * * * *", Timezone: "Asia/Shanghai",
			ChatID: "chat", Channel: "feishu", Prompt: "hi", Enabled: true, NextRunAt: &tc.next,
		}); err != nil {
			t.Fatal(err)
		}
	}

	claimed, err := j.ClaimDue(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != "test_due" {
		t.Fatalf("claimed = %+v", claimed)
	}

	next := time.Now().Add(time.Hour)
	if err := j.FinishTask(ctx, "test_due", next, "ok"); err != nil {
		t.Fatal(err)
	}
	task, err := j.GetTask(ctx, "test_due")
	if err != nil {
		t.Fatal(err)
	}
	if task.LastStatus != "ok" || task.NextRunAt == nil || task.NextRunAt.Before(time.Now()) {
		t.Fatalf("task = %+v", task)
	}
	// 第二次抢占应为空，避免重复执行。
	claimed, err = j.ClaimDue(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("不应重复抢占: %+v", claimed)
	}
}

// TestStaleClaimRecovery 覆盖领取后进程崩溃的场景：next_run_at 已置空、
// claimed_at 过期，恢复扫描要能把任务重新排上，而不是永久停摆。
func TestStaleClaimRecovery(t *testing.T) {
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
	if _, err := db.Pool().Exec(ctx, `DELETE FROM scheduled_task WHERE id LIKE 'test_%'`); err != nil {
		t.Fatal(err)
	}

	j := store.NewJournal(db)
	past := time.Now().Add(-time.Minute)
	if err := j.SaveTask(ctx, store.ScheduledTask{
		ID: "test_stale", Name: "test_stale", Cron: "*/5 * * * *", Timezone: "Asia/Shanghai",
		ChatID: "chat", Channel: "feishu", Prompt: "hi", Enabled: true, NextRunAt: &past,
	}); err != nil {
		t.Fatal(err)
	}

	// 模拟领取后崩溃：next_run_at 置空，claimed_at 停在很久以前。
	claimed, err := j.ClaimDue(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed = %+v", claimed)
	}
	// 刚领取的任务不应被回收。
	if stale, err := j.RecoverStaleClaims(ctx, time.Now(), store.ClaimStaleAfter); err != nil {
		t.Fatal(err)
	} else if len(stale) != 0 {
		t.Fatalf("新鲜领取不应被回收: %+v", stale)
	}

	// 模拟领取后崩溃：claimed_at 停在很久以前。
	if _, err := db.Pool().Exec(ctx, `UPDATE scheduled_task SET claimed_at = $1 WHERE id = 'test_stale'`, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	stale, err := j.RecoverStaleClaims(ctx, time.Now(), store.ClaimStaleAfter)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].ID != "test_stale" {
		t.Fatalf("stale = %+v", stale)
	}

	next := time.Now().Add(5 * time.Minute)
	if err := j.ReleaseClaim(ctx, "test_stale", next, "recovered"); err != nil {
		t.Fatal(err)
	}
	task, err := j.GetTask(ctx, "test_stale")
	if err != nil {
		t.Fatal(err)
	}
	if task.NextRunAt == nil || task.NextRunAt.Before(time.Now()) {
		t.Fatalf("恢复后应重新排期: %+v", task)
	}
	if task.LastStatus != "recovered" {
		t.Fatalf("last_status = %q", task.LastStatus)
	}
	// 恢复不应伪造执行时间。
	if task.LastRunAt != nil {
		t.Fatalf("恢复后 last_run_at 应为空: %+v", task.LastRunAt)
	}
}
