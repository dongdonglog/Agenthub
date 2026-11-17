package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/store"
)

func TestEscalateOverdueCase(t *testing.T) {
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE case_item, case_event, employee`); err != nil {
		t.Fatal(err)
	}
	cases := store.NewCaseStore(db)
	now := time.Now()
	c, _, err := cases.EnsureCase(ctx, "sre-oncall", "incident", "test", "sla-1", "SLA test", "", "P1", "", "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `UPDATE case_item SET sla_due_at=$2 WHERE id=$1`, c.ID, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	ids, err := cases.EscalateOverdue(ctx, now, 10)
	if err != nil || len(ids) != 1 || ids[0] != c.ID {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	got, err := cases.GetCase(ctx, c.ID)
	if err != nil || got.Status != "waiting_human" {
		t.Fatalf("case=%+v err=%v", got, err)
	}
	events, err := cases.ListCaseEvents(ctx, c.ID, 10)
	if err != nil || len(events) != 1 || events[0].Kind != "sla" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}
