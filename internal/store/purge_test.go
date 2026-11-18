package store_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/core"
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

// TestPurgeBeforeKeepsRunningRows 守住「运行中的行不能被保留期清理删掉」。
//
// @remarks 真实踩过的坑：SaveRun 曾把 Go 零值时间写进 ended_at，落库成
// 0001-01-01 而不是 NULL，于是 `ended_at IS NOT NULL AND ended_at < cutoff`
// 会把正在运行的记录当成过期数据删除。卡片句柄随之消失，那张「处理中」卡片
// 就永远没人能关。
func TestPurgeBeforeKeepsRunningRows(t *testing.T) {
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

	journal := store.NewJournal(db)
	// 运行中：EndedAt 是零值，HeartbeatAt 正常刷新。
	running := &core.Run{
		ID: "run_running", ConversationID: "c1", ChatID: "chat",
		State: "running", StartedAt: time.Now(), CardHandle: "om_card_1",
		HeartbeatAt: time.Now(),
	}
	if err := journal.SaveRun(ctx, running); err != nil {
		t.Fatal(err)
	}

	if _, err := journal.PurgeBefore(ctx, time.Now().AddDate(0, 0, -7)); err != nil {
		t.Fatal(err)
	}

	var endedNull bool
	var handle string
	if err := db.Pool().QueryRow(ctx,
		`SELECT ended_at IS NULL, card_handle FROM run_state WHERE id = 'run_running'`).Scan(&endedNull, &handle); err != nil {
		t.Fatalf("运行中的行被清理删除了（卡片将永远停在处理中）: %v", err)
	}
	if !endedNull {
		t.Fatal("零值 EndedAt 被写成了具体时间，会被保留期清理误删")
	}
	if handle != "om_card_1" {
		t.Fatalf("card_handle = %q, want om_card_1", handle)
	}

	// 已结束的旧运行仍然要被清掉，避免把清理本身弄坏。
	oldEnded := time.Now().AddDate(0, 0, -30)
	if _, err := db.Pool().Exec(ctx,
		`INSERT INTO run_state (id, conversation_id, state, started_at, ended_at) VALUES ('run_old','c1','completed',$1,$1)`,
		oldEnded); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.PurgeBefore(ctx, time.Now().AddDate(0, 0, -7)); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM run_state WHERE id = 'run_old'`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatal("过期运行应被清理")
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
