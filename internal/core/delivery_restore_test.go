package core_test

import (
	"context"
	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
	"testing"
)

func TestSendingDeliveryBecomesAmbiguousOnRestart(t *testing.T) {
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
	if _, err := db.Pool().Exec(ctx, `
		INSERT INTO delivery_state
			(idempotency_key, run_id, chat_id, reply_to, text, state, attempts, created_at, send_started_at)
		VALUES ('run_x:result', 'run_x', 'oc_1', 'om_1', '结果', 'sending', 1, now(), now())`); err != nil {
		t.Fatal(err)
	}

	ch := &captureChannel{}
	journal := store.NewJournal(db)
	engine := core.New(agent.Echo{}, ch, nil)
	engine.SetJournal(journal)
	if err := engine.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if ch.count() != 0 {
		t.Fatalf("歧义投递不应自动重发，实际发送 %d 次", ch.count())
	}
	var state string
	if err := db.Pool().QueryRow(ctx, `SELECT state FROM delivery_state WHERE idempotency_key = 'run_x:result'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "ambiguous" {
		t.Fatalf("state = %s, want ambiguous", state)
	}
	items := engine.DeliveriesFor("run_x")
	if len(items) != 1 || items[0].State != "ambiguous" {
		t.Fatalf("deliveries = %+v", items)
	}
}

func TestSendPersistsSendingBeforeChannelCall(t *testing.T) {
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

	ch := &probeChannel{db: db}
	engine := core.New(agent.Echo{}, ch, nil)
	engine.SetJournal(store.NewJournal(db))
	if _, err := engine.Submit(ctx, inbound("om_probe", "你好")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if ch.seen != "sending" {
		t.Fatalf("发送前数据库状态 = %q, want sending", ch.seen)
	}
}

type probeChannel struct {
	db   *store.DB
	seen string
}

func (c *probeChannel) SendText(ctx context.Context, _ channel.Outbound) (channel.Receipt, error) {
	_ = c.db.Pool().QueryRow(ctx, `SELECT state FROM delivery_state LIMIT 1`).Scan(&c.seen)
	return channel.Receipt{}, nil
}
