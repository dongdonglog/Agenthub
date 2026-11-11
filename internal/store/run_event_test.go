package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

func TestRunEventRoundTrip(t *testing.T) {
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE run_trace`); err != nil {
		t.Fatal(err)
	}
	j := store.NewJournal(db)
	ev := core.RunEvent{RunID: "run_x", Kind: "tool", Name: "demo", Detail: "ok", DurationMS: 12, CreatedAt: time.Now()}
	if err := j.SaveRunEvent(ctx, ev); err != nil {
		t.Fatalf("SaveRunEvent: %v", err)
	}
	events, err := j.LoadRunEvents(ctx, "run_x")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Name != "demo" {
		t.Fatalf("events = %+v", events)
	}
}
