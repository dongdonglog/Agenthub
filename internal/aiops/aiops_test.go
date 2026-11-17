package aiops

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

type captureChannel struct{ sent int }

func (c *captureChannel) SendText(context.Context, channel.Outbound) (channel.Receipt, error) {
	c.sent++
	return channel.Receipt{ExternalID: "om_ops"}, nil
}

func TestSeedAndPrompt(t *testing.T) {
	payload := AlertmanagerPayload{
		GroupKey: "group-1", Status: "firing",
		CommonLabels:      map[string]string{"service": "api", "severity": "critical", "alertname": "HighErrorRate"},
		CommonAnnotations: map[string]string{"summary": "错误率过高", "description": "5xx 超过 5%"},
		Alerts: []Alert{{Status: "firing",
			Labels:      map[string]string{"alertname": "HighErrorRate", "service": "api", "severity": "critical"},
			Annotations: map[string]string{"summary": "错误率过高", "description": "5xx 超过 5%"},
			StartsAt:    time.Now(), Fingerprint: "fp1"}},
	}
	seed := seedOf(payload)
	if seed.Service != "api" || seed.Severity != "critical" || seed.Title != "错误率过高" {
		t.Fatalf("seed = %+v", seed)
	}
	i := &Ingestor{cfg: Config{MaxAlerts: 10}, now: time.Now}
	prompt := i.buildPrompt(&store.Incident{ID: "inc_1", Title: seed.Title, Severity: seed.Severity, Service: seed.Service}, payload)
	if !strings.Contains(prompt, "HighErrorRate") || !strings.Contains(prompt, "只读诊断") {
		t.Fatalf("prompt = %s", prompt)
	}
}

func TestIngestCreatesIncidentAndDiagnosisRun(t *testing.T) {
	url := os.Getenv("AGENTHUB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 AGENTHUB_TEST_DATABASE_URL，跳过 AIOps 数据库用例")
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE incident, alert_event, incident_event, case_item, case_event, employee`); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ch := &captureChannel{}
	engine := core.New(agent.Echo{}, ch, logger)
	caseStore := store.NewCaseStore(db)
	if _, err := caseStore.EnsureDefaultEmployee(ctx, "oc_ops", time.Now()); err != nil {
		t.Fatal(err)
	}
	ingestor := NewIngestor(store.NewIncidentStore(db), engine, Config{ChatID: "oc_ops", MaxAlerts: 10}, logger, nil)
	ingestor.SetCases(caseStore, "sre-oncall")

	body := `{"version":"4","groupKey":"group-1","status":"firing","commonLabels":{"service":"api","severity":"critical"},"commonAnnotations":{"summary":"错误率过高"},"alerts":[{"status":"firing","labels":{"alertname":"HighErrorRate","service":"api"},"annotations":{"summary":"错误率过高"},"startsAt":"2026-11-16T00:00:00Z","fingerprint":"fp1"}]}`
	if n, err := ingestor.Handle(ctx, []byte(body)); err != nil || n != 1 {
		t.Fatalf("handle n=%d err=%v", n, err)
	}
	items, err := store.NewIncidentStore(db).ListIncidents(ctx, 10)
	if err != nil || len(items) != 1 || items[0].Status != "firing" || items[0].AlertCount != 1 {
		t.Fatalf("incidents = %+v err=%v", items, err)
	}
	msgs := engine.Messages("aiops:oc_ops:alertmanager")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "错误率过高") || !msgs[0].NoReply {
		t.Fatalf("messages = %+v", msgs)
	}

	// 重复 webhook 只更新时间线，不重复触发诊断。
	if _, err := ingestor.Handle(ctx, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if got := len(engine.Messages("aiops:oc_ops:alertmanager")); got != 1 {
		t.Fatalf("duplicate should not create run, messages=%d", got)
	}

	resolved := strings.Replace(body, `"status":"firing","commonLabels"`, `"status":"resolved","commonLabels"`, 1)
	if _, err := ingestor.Handle(ctx, []byte(resolved)); err != nil {
		t.Fatal(err)
	}
	items, _ = store.NewIncidentStore(db).ListIncidents(ctx, 10)
	if len(items) != 1 || items[0].Status != "resolved" {
		t.Fatalf("resolved = %+v", items)
	}
	cases, err := caseStore.ListCases(ctx, 10)
	if err != nil || len(cases) != 1 || cases[0].Status != "resolved" {
		t.Fatalf("cases = %+v err=%v", cases, err)
	}
	if err := caseStore.AddFeedback(ctx, cases[0].ID, "oncall", "useful", "根因正确", 5, time.Now()); err != nil {
		t.Fatal(err)
	}
	stats, err := caseStore.EmployeeStats(ctx, "sre-oncall")
	if err != nil || stats.Cases != 1 || stats.Resolved != 1 || stats.Useful != 1 || stats.CSAT != 5 {
		t.Fatalf("stats = %+v err=%v", stats, err)
	}
	if cases[0].SLADueAt == nil {
		t.Fatal("case SLA due not set")
	}
}

func TestServiceCatalogLookup(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/services.yaml"
	if err := os.WriteFile(path, []byte("services:\n  checkout-api:\n    owner: checkout\n    team: payments\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	svc, ok := catalog.Lookup("Checkout-API")
	if !ok || svc.Owner != "checkout" || svc.Team != "payments" {
		t.Fatalf("svc = %+v ok=%v", svc, ok)
	}
}
