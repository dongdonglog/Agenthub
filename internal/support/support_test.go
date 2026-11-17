package support

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

type captureChannel struct{}

func (captureChannel) SendText(context.Context, channel.Outbound) (channel.Receipt, error) {
	return channel.Receipt{ExternalID: "om_support"}, nil
}

func TestTicketCreatesCaseAndSupportRun(t *testing.T) {
	url := os.Getenv("AGENTHUB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 AGENTHUB_TEST_DATABASE_URL，跳过支持工单数据库用例")
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE case_item, case_event, case_feedback, employee, approval_request, knowledge_doc`); err != nil {
		t.Fatal(err)
	}
	caseStore := store.NewCaseStore(db)
	if _, err := caseStore.EnsureEmployee(ctx, store.Employee{ID: "tech-support", Name: "技术支持"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := core.New(agent.Echo{}, captureChannel{}, logger)
	knowledgeStore := store.NewKnowledgeStore(db)
	if err := knowledgeStore.Upsert(ctx, store.KnowledgeDoc{Title: "登录 500", Content: "先检查 auth 服务与上游 500", Source: "kb/login.md"}); err != nil {
		t.Fatal(err)
	}
	ingestor := New(caseStore, engine, Config{ChatID: "oc_support", EmployeeID: "tech-support"}, logger)
	ingestor.SetKnowledge(knowledgeStore)

	raw := []byte(`{"id":"T-1","title":"无法登录","description":"用户登录 500","requester":"ou_1","priority":"P1","service":"account","environment":"prod"}`)
	id, err := ingestor.Handle(ctx, raw)
	if err != nil || id == "" {
		t.Fatalf("id=%s err=%v", id, err)
	}
	cases, err := caseStore.ListCases(ctx, 10)
	if err != nil || len(cases) != 1 || cases[0].Type != "ticket" || cases[0].Status != "in_progress" {
		t.Fatalf("cases = %+v err=%v", cases, err)
	}
	msgs := engine.Messages("support:oc_support:ticket")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "无法登录") || !msgs[0].ReadOnly || !strings.Contains(msgs[0].Text, "登录 500") || !strings.Contains(msgs[0].Text, "kb/login.md") {
		t.Fatalf("messages = %+v", msgs)
	}
}
