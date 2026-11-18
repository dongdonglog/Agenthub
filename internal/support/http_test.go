package support

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

const ticketBody = `{"id":"tk-http","title":"登录失败","description":"用户无法登录","requester":"alice","priority":"high","service":"auth"}`

// newTicketHandler 准备一个可用的工单入口。
func newTicketHandler(t *testing.T, token string) *Handler {
	t.Helper()
	url := os.Getenv("AGENTHUB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 AGENTHUB_TEST_DATABASE_URL，跳过工单入口数据库用例")
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE case_item, case_event, case_feedback, employee, approval_request, knowledge_doc`); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := core.New(agent.Echo{}, captureChannel{}, logger)
	caseStore := store.NewCaseStore(db)
	if _, err := caseStore.EnsureDefaultEmployee(ctx, "oc_support", time.Now()); err != nil {
		t.Fatal(err)
	}
	ingestor := New(caseStore, engine, Config{ChatID: "oc_support"}, logger)
	return NewHandler(ingestor, token)
}

func serveTicket(h http.Handler, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tickets", strings.NewReader(body))
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestTicketWebhookAccepts 覆盖工单入口的受理路径与 case_id 回显。
func TestTicketWebhookAccepts(t *testing.T) {
	h := newTicketHandler(t, "")

	rec := serveTicket(h, ticketBody, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"accepted"`) || !strings.Contains(body, `"case_id":"case_`) {
		t.Fatalf("body = %s", body)
	}
}

// TestTicketWebhookRejectsBadToken 覆盖两种 token 传递方式与拒绝路径。
func TestTicketWebhookRejectsBadToken(t *testing.T) {
	h := newTicketHandler(t, "secret")

	if rec := serveTicket(h, ticketBody, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401，实际 %d", rec.Code)
	}
	if rec := serveTicket(h, ticketBody, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer nope")
	}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误 token 应 401，实际 %d", rec.Code)
	}
	if rec := serveTicket(h, ticketBody, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer secret")
	}); rec.Code != http.StatusAccepted {
		t.Fatalf("Bearer 正确应受理，实际 %d", rec.Code)
	}
	if rec := serveTicket(h, ticketBody, func(r *http.Request) {
		r.Header.Set("X-Ticket-Token", "secret")
	}); rec.Code != http.StatusAccepted {
		t.Fatalf("header token 应受理，实际 %d", rec.Code)
	}
}

// TestTicketWebhookRejectsMalformed 载荷非法时 400。
func TestTicketWebhookRejectsMalformed(t *testing.T) {
	h := newTicketHandler(t, "")

	rec := serveTicket(h, "{not json", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法载荷应 400，实际 %d", rec.Code)
	}
}

// TestTicketWebhookDisabled 未接入时 503。
func TestTicketWebhookDisabled(t *testing.T) {
	h := NewHandler(nil, "")
	if rec := serveTicket(h, ticketBody, nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("未启用应 503，实际 %d", rec.Code)
	}
}
