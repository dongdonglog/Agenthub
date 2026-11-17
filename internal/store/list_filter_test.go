package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/store"
)

func TestCaseAndIncidentFilters(t *testing.T) {
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE case_item, case_event, case_review, incident, incident_event, alert_event, employee`); err != nil {
		t.Fatal(err)
	}
	cases := store.NewCaseStore(db)
	now := time.Now()
	if _, _, err := cases.EnsureCase(ctx, "sre-oncall", "incident", "test", "f1", "支付错误", "", "P1", "pay", "prod", "", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cases.EnsureCase(ctx, "tech-support", "ticket", "test", "f2", "登录问题", "", "P3", "account", "prod", "", now); err != nil {
		t.Fatal(err)
	}
	items, total, err := cases.ListCasesFiltered(ctx, store.CaseFilter{Type: "ticket", Query: "登录", Limit: 10})
	if err != nil || total != 1 || len(items) != 1 || items[0].Type != "ticket" {
		t.Fatalf("items=%+v total=%d err=%v", items, total, err)
	}

	if err := cases.AddReview(ctx, store.CaseReview{CaseID: items[0].ID, Reviewer: "alice", Evidence: 2, RootCause: 1, Safety: 3, Usefulness: 2, Honesty: 3}, now); err != nil {
		t.Fatal(err)
	}
	reviews, err := cases.ListReviews(ctx, items[0].ID)
	if err != nil || len(reviews) != 1 || reviews[0].Evidence != 2 {
		t.Fatalf("reviews=%+v err=%v", reviews, err)
	}
	stats, err := cases.EmployeeStats(ctx, "tech-support")
	if err != nil || stats.Reviews != 1 || stats.AvgSafety != 3 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}

	incidents := store.NewIncidentStore(db)
	seed := store.IncidentSeed{Title: "支付错误", Service: "pay", Severity: "critical"}
	inc, _, err := incidents.UpsertIncident(ctx, "g1", seed, store.AlertRecord{Fingerprint: "fp1", Status: "firing", AlertName: "Pay", Severity: "critical"}, now)
	if err != nil {
		t.Fatal(err)
	}
	list, total, err := incidents.ListIncidentsFiltered(ctx, store.IncidentFilter{Status: "firing", Query: "支付", Limit: 10})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("list=%+v total=%d err=%v", list, total, err)
	}
	resolved, ok, err := incidents.ResolveIncidentByID(ctx, inc.ID, "admin", now)
	if err != nil || !ok || resolved.Status != "resolved" {
		t.Fatalf("resolved=%+v ok=%v err=%v", resolved, ok, err)
	}
}

func TestCaseReportRoundTrip(t *testing.T) {
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE case_item, case_report, case_event, employee`); err != nil {
		t.Fatal(err)
	}
	cases := store.NewCaseStore(db)
	now := time.Now()
	c, _, err := cases.EnsureCase(ctx, "sre-oncall", "incident", "test", "r1", "支付故障", "", "P1", "pay", "prod", "", now)
	if err != nil {
		t.Fatal(err)
	}
	report := store.CaseReport{CaseID: c.ID, Problem: "支付 5xx", RootCause: "连接池耗尽", Verification: "错误率恢复", Status: "final"}
	if err := cases.SaveCaseReport(ctx, report, now); err != nil {
		t.Fatal(err)
	}
	got, err := cases.GetCaseReport(ctx, c.ID)
	if err != nil || got == nil || got.RootCause != "连接池耗尽" || got.Status != "final" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	list, err := cases.ListCaseReports(ctx, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
}
