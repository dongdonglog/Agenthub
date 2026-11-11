package store_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/store"
)

func TestPurgeBeforeKeepsActiveRows(t *testing.T) {
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE message_state, run_state, delivery_state, run_trace`); err != nil {
		t.Fatal(err)
	}

	old := time.Now().AddDate(0, 0, -30)
	_, err = db.Pool().Exec(ctx, `INSERT INTO message_state (id, role, generation, conversation_id, chat_id, external_id, status, created_at)
		VALUES ('old-done','user',1,'c1','chat','e1','done',$1), ('old-queued','user',1,'c1','chat','e2','queued',$1)`, old)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool().Exec(ctx, `INSERT INTO delivery_state (idempotency_key, run_id, chat_id, state, created_at)
		VALUES ('old-sent','r1','c1','sent',$1), ('old-pending','r2','c1','pending',$1)`, old)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.Pool().Exec(ctx, `INSERT INTO run_state (id, conversation_id, state, ended_at) VALUES ('r1','c1','completed',$1)`, old)
	_, _ = db.Pool().Exec(ctx, `INSERT INTO run_trace (run_id, kind, created_at) VALUES ('r1','phase',$1)`, old)

	deleted, err := store.NewJournal(db).PurgeBefore(ctx, time.Now().AddDate(0, 0, -7))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 4 {
		t.Fatalf("deleted = %d, want 4", deleted)
	}

	var count int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM message_state`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("剩下消息数 = %d，期望只保留排队那条", count)
	}
}

// testDatabaseURL 返回测试库地址；未配置或指向应用库时跳过。
//
// @remarks 测试会清空表数据，必须使用独立数据库，避免误删线上/开发数据。
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
