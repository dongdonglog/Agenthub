package store_test

import (
	"context"
	"testing"

	"github.com/moying/agenthub/internal/store"
)

func TestPriorityRoundTrip(t *testing.T) {
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
	if _, err := db.Pool().Exec(ctx, `DELETE FROM conversation_state WHERE id = 'prio:test:u1'`); err != nil {
		t.Fatal(err)
	}

	j := store.NewJournal(db)
	if err := j.SavePriority(ctx, "prio:test:u1", 42); err != nil {
		t.Fatal(err)
	}
	if err := j.SavePriority(ctx, "prio:test:u2", 0); err != nil {
		t.Fatal(err)
	}

	got, err := j.LoadPriorities(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got["prio:test:u1"] != 42 {
		t.Fatalf("u1 = %d", got["prio:test:u1"])
	}
	if v, ok := got["prio:test:u2"]; !ok || v != 0 {
		t.Fatalf("显式的 0 应保留: %v %v", v, ok)
	}

	// upsert 覆盖
	if err := j.SavePriority(ctx, "prio:test:u1", 7); err != nil {
		t.Fatal(err)
	}
	got, _ = j.LoadPriorities(ctx, 1000)
	if got["prio:test:u1"] != 7 {
		t.Fatalf("覆盖失败: %d", got["prio:test:u1"])
	}
}
