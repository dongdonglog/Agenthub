package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

// TestRestoreKeepsMessageSemantics 覆盖重启后语义字段丢失的问题。
//
// @remarks 历史上 message_state 只存展示字段，恢复后 NoReply/ReadOnly/ToolAllow/CaseID
// 全空，导致：内部合成 ID（incident:xxx:firing）被当成飞书 message_id 去回复，
// 投递必然 400；只读诊断也退化成可写运行。这里锁住恢复后的四个字段。
func TestRestoreKeepsMessageSemantics(t *testing.T) {
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE message_state, run_state, delivery_state`); err != nil {
		t.Fatal(err)
	}

	journal := store.NewJournal(db)
	eng := core.New(agent.Echo{}, &captureChannel{}, nil)
	eng.SetJournal(journal)

	in := channel.Inbound{
		Channel: "aiops", Tenant: "aiops",
		ConversationKey: "aiops:inc_sem",
		ExternalID:      "incident:inc_sem:firing",
		SentAt:          time.Now(),
		Sender:          channel.Sender{OpenID: "alertmanager", Name: "alertmanager"},
		Chat:            channel.Chat{ID: "oc_ops", Kind: "group"},
		Text:            "诊断这次告警",
		MsgType:         "text",
		CaseID:          "case_sem",
		ReadOnly:        true,
		NoReply:         true,
		ToolAllow:       []string{"read", "grep", "ops_investigate"},
	}
	if _, err := eng.Submit(ctx, in); err != nil {
		t.Fatal(err)
	}

	restored := core.New(agent.Echo{}, nil, nil)
	restored.SetJournal(journal)
	if err := restored.Restore(ctx); err != nil {
		t.Fatal(err)
	}

	msgs := restored.Messages("aiops:inc_sem")
	if len(msgs) != 1 {
		t.Fatalf("恢复消息数 = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if !m.NoReply {
		t.Fatal("NoReply 丢失：恢复后会把内部合成 ID 当成平台消息去回复")
	}
	if !m.ReadOnly {
		t.Fatal("ReadOnly 丢失：只读诊断会退化成可写运行")
	}
	if m.CaseID != "case_sem" {
		t.Fatalf("CaseID = %q, want case_sem", m.CaseID)
	}
	if len(m.ToolAllow) != 3 || m.ToolAllow[0] != "read" || m.ToolAllow[2] != "ops_investigate" {
		t.Fatalf("ToolAllow = %v", m.ToolAllow)
	}
}
