package core_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

func TestJournalPersistenceAndRestore(t *testing.T) {
	url := os.Getenv("AGENTHUB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 AGENTHUB_TEST_DATABASE_URL，跳过真实数据库用例")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `TRUNCATE message_state, run_state, delivery_state`); err != nil {
		t.Fatal(err)
	}

	journal := store.NewJournal(db)
	ch := &captureChannel{}
	engine := core.New(agent.Echo{}, ch, nil)
	engine.SetJournal(journal)

	for _, text := range []string{"第一条", "第二条"} {
		if _, err := engine.Submit(ctx, channel.Inbound{
			Channel: "feishu", Tenant: "t", ExternalID: "ext-" + text,
			Chat:   channel.Chat{ID: "chat-1", Kind: "group"},
			Sender: channel.Sender{OpenID: "ou-1"},
			Text:   text, MsgType: "text", SentAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 重复外部 ID 不再写入。
	if ok, _ := engine.Submit(ctx, channel.Inbound{
		Channel: "feishu", Tenant: "t", ExternalID: "ext-第一条",
		Chat:   channel.Chat{ID: "chat-1", Kind: "group"},
		Sender: channel.Sender{OpenID: "ou-1"}, Text: "重复",
	}); ok {
		t.Fatal("重复消息不应被受理")
	}
	if err := engine.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	// 新引擎从库里恢复，应能看到历史消息与运行，且没有残留排队。
	restored := core.New(agent.Echo{}, nil, nil)
	restored.SetJournal(journal)
	if err := restored.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	msgs := restored.Messages("t:chat-1:ou-1")
	if len(msgs) != 2 {
		t.Fatalf("恢复消息数 = %d, want 2", len(msgs))
	}
	for _, m := range msgs {
		if m.Status != "done" {
			t.Fatalf("恢复状态 = %s, want done", m.Status)
		}
	}
	if runs := restored.Runs(); len(runs) != 1 {
		t.Fatalf("恢复运行数 = %d, want 1", len(runs))
	}
}
