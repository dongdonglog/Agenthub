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
