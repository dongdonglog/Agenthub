package aiops

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

const alertmanagerBody = `{"version":"4","groupKey":"g-http","status":"firing",` +
	`"commonLabels":{"service":"api","severity":"critical"},` +
	`"commonAnnotations":{"summary":"错误率过高"},` +
	`"alerts":[{"status":"firing","labels":{"alertname":"HighErrorRate","service":"api"},` +
	`"annotations":{"summary":"错误率过高"},"startsAt":"2026-11-16T00:00:00Z","fingerprint":"fp-http"}]}`

// newHTTPHarness 准备一个可用的告警入口。
func newHTTPHarness(t *testing.T) (*Handler, *core.Engine) {
	t.Helper()
	url := os.Getenv("AGENTHUB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 AGENTHUB_TEST_DATABASE_URL，跳过告警入口数据库用例")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := store.Migrate(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `TRUNCATE incident, alert_event, incident_event, case_item, case_event, employee`); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := core.New(agent.Echo{}, &captureChannel{}, logger)
	ingestor := NewIngestor(store.NewIncidentStore(db), engine, Config{ChatID: "oc_ops", MaxAlerts: 10}, logger, nil)
	return NewHandler(ingestor, ""), engine
}

// serve 走真实的 ServeMux，确保 {source} 路径变量被正确填充。
func serve(h http.Handler, method, path, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/alerts/{source}", h)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestAlertWebhookAcceptsAndCreatesIncident 覆盖「受理」不只是回 ACK。
func TestAlertWebhookAcceptsAndCreatesIncident(t *testing.T) {
	h, engine := newHTTPHarness(t)

	rec := serve(h, http.MethodPost, "/api/v1/alerts/alertmanager", alertmanagerBody, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"alerts":1`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"source":"alertmanager"`) {
		t.Fatalf("source 应回显来源: %s", rec.Body.String())
	}
	// 受理必须真的落下事故与诊断会话，而不是只返回 202。
	if !hasAiopsSession(engine) {
		t.Fatal("受理后应出现 aiops: 诊断会话")
	}
}

func hasAiopsSession(e *core.Engine) bool {
	for _, info := range e.SessionInfos() {
		if strings.HasPrefix(info.ID, "aiops:") {
			return true
		}
	}
	return false
}

func TestAlertWebhookRejectsBadToken(t *testing.T) {
	h, _ := newHTTPHarness(t)
	h = NewHandler(h.ingestor, "secret")

	rec := serve(h, http.MethodPost, "/api/v1/alerts/alertmanager", alertmanagerBody, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401，实际 %d", rec.Code)
	}

	wrong := serve(h, http.MethodPost, "/api/v1/alerts/alertmanager", alertmanagerBody, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer nope")
	})
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("错误 token 应 401，实际 %d", wrong.Code)
	}

	ok := serve(h, http.MethodPost, "/api/v1/alerts/alertmanager", alertmanagerBody, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer secret")
	})
	if ok.Code != http.StatusAccepted {
		t.Fatalf("正确 token 应受理，实际 %d: %s", ok.Code, ok.Body.String())
	}
}

// TestAlertWebhookAcceptsAlertmanagerHeaderToken 覆盖 Alertmanager 常用 header。
func TestAlertWebhookAcceptsAlertmanagerHeaderToken(t *testing.T) {
	h, _ := newHTTPHarness(t)
	h = NewHandler(h.ingestor, "secret")

	rec := serve(h, http.MethodPost, "/api/v1/alerts/alertmanager", alertmanagerBody, func(r *http.Request) {
		r.Header.Set("X-Alertmanager-Token", "secret")
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("header token 应受理，实际 %d", rec.Code)
	}
}

// TestAlertWebhookSourceTokenOverridesGlobal 确认按来源的 token 优先于全局 token。
func TestAlertWebhookSourceTokenOverridesGlobal(t *testing.T) {
	h, _ := newHTTPHarness(t)
	h = NewHandler(h.ingestor, "global")
	h.SetSourceTokens(map[string]string{"generic": "per-source"})

	withGlobal := serve(h, http.MethodPost, "/api/v1/alerts/generic", alertmanagerBody, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer global")
	})
	if withGlobal.Code != http.StatusUnauthorized {
		t.Fatalf("该来源配置了独立 token 时，全局 token 应失效，实际 %d", withGlobal.Code)
	}

	withSource := serve(h, http.MethodPost, "/api/v1/alerts/generic", alertmanagerBody, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer per-source")
	})
	if withSource.Code != http.StatusAccepted {
		t.Fatalf("来源 token 应受理，实际 %d", withSource.Code)
	}
}

// TestAlertWebhookRateLimited 覆盖按来源限流：额度耗尽后返回 429。
func TestAlertWebhookRateLimited(t *testing.T) {
	h, _ := newHTTPHarness(t)
	h.SetRateLimit(1)

	first := serve(h, http.MethodPost, "/api/v1/alerts/alertmanager", alertmanagerBody, nil)
	if first.Code != http.StatusAccepted {
		t.Fatalf("首次应受理，实际 %d", first.Code)
	}
	second := serve(h, http.MethodPost, "/api/v1/alerts/alertmanager", alertmanagerBody, nil)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("额度耗尽应 429，实际 %d", second.Code)
	}
}

// TestAlertWebhookRejectsMalformed 载荷无法解析时应 400 而不是 500。
func TestAlertWebhookRejectsMalformed(t *testing.T) {
	h, _ := newHTTPHarness(t)

	rec := serve(h, http.MethodPost, "/api/v1/alerts/alertmanager", "{not json", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法载荷应 400，实际 %d", rec.Code)
	}
}

// TestAlertWebhookDisabled 未接入 AIOps 时应 503。
func TestAlertWebhookDisabled(t *testing.T) {
	h := NewHandler(nil, "")
	rec := serve(h, http.MethodPost, "/api/v1/alerts/alertmanager", alertmanagerBody, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("未启用应 503，实际 %d", rec.Code)
	}
}
