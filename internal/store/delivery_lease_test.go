package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

func TestDeliveryLeaseClaimAndFinish(t *testing.T) {
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE delivery_state, run_artifact, run_trace, message_state, run_state`); err != nil {
		t.Fatal(err)
	}
	j := store.NewJournal(db)
	if err := j.SaveDelivery(ctx, &core.Delivery{
		IdempotencyKey: "key", RunID: "run", ChatID: "chat", Text: "hello", State: "pending", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	first, ok, err := j.ClaimDelivery(ctx, "key", "owner-1", "token-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("first claim ok=%v err=%v", ok, err)
	}
	if first.State != "sending" || first.SendPhase != "claimed" || first.Attempts != 1 || first.LeaseToken != "token-1" {
		t.Fatalf("claimed = %+v", first)
	}
	if _, ok, _ := j.ClaimDelivery(ctx, "key", "owner-2", "token-2", time.Minute); ok {
		t.Fatal("同一投递不应被第二次领取")
	}
	if ok, err := j.MarkDeliveryCalling(ctx, "key", "wrong"); err != nil || ok {
		t.Fatalf("错误令牌不应推进阶段: ok=%v err=%v", ok, err)
	}
	if ok, err := j.MarkDeliveryCalling(ctx, "key", "token-1"); err != nil || !ok {
		t.Fatalf("mark calling ok=%v err=%v", ok, err)
	}
	if ok, err := j.FinishDelivery(ctx, "key", "wrong", "sent", "", "m1", time.Time{}); err != nil || ok {
		t.Fatalf("错误令牌不应完成: ok=%v err=%v", ok, err)
	}
	if ok, err := j.FinishDelivery(ctx, "key", "token-1", "sent", "", "m1", time.Time{}); err != nil || !ok {
		t.Fatalf("finish ok=%v err=%v", ok, err)
	}
	var state, external string
	if err := db.Pool().QueryRow(ctx, `SELECT state, external_id FROM delivery_state WHERE idempotency_key='key'`).Scan(&state, &external); err != nil {
		t.Fatal(err)
	}
	if state != "sent" || external != "m1" {
		t.Fatalf("state=%s external=%s", state, external)
	}
}
