package feishu_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/platform/feishu"
)

type blockingAgent struct {
	once    sync.Once
	started chan struct{}
}

func (a *blockingAgent) Run(ctx context.Context, _ agent.Input) (agent.Output, error) {
	a.once.Do(func() { close(a.started) })
	<-ctx.Done()
	return agent.Output{}, ctx.Err()
}

func startRun(t *testing.T) (*core.Engine, *blockingAgent, chan struct{}, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &blockingAgent{started: make(chan struct{})}
	engine := core.New(a, nil, logger)
	if _, err := engine.Submit(context.Background(), channel.Inbound{
		Channel: "feishu", Tenant: "t", ExternalID: "e1",
		Chat:   channel.Chat{ID: "c1", Kind: "p2p"},
		Sender: channel.Sender{OpenID: "ou_owner"}, Text: "长任务",
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = engine.Flush(context.Background()) }()
	select {
	case <-a.started:
	case <-time.After(2 * time.Second):
		t.Fatal("运行未启动")
	}
	// 运行 ID 带随机后缀，测试必须用实际值而不是写死 run_1。
	runs := engine.Runs()
	if len(runs) != 1 {
		t.Fatalf("运行数 = %d, want 1", len(runs))
	}
	return engine, a, done, runs[0].ID
}

func postCard(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/feishu/card", bytes.NewBufferString(body)))
	return rec
}

func TestCardStopByOwner(t *testing.T) {
	engine, _, done, runID := startRun(t)
	h := feishu.NewCardAction(engine, "vt", "", nil)

	body := `{"header":{"token":"vt"},"event":{"operator":{"open_id":"ou_owner"},"action":{"value":{"action":"stop","run_id":"` + runID + `"}}}}`
	rec := postCard(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("停止后运行未结束")
	}
	runs := engine.Runs()
	if len(runs) != 1 || runs[0].State != "interrupted" {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestCardStopRejectsOthers(t *testing.T) {
	engine, _, done, runID := startRun(t)
	h := feishu.NewCardAction(engine, "vt", "", nil)

	body := `{"header":{"token":"vt"},"event":{"operator":{"open_id":"ou_other"},"action":{"value":{"action":"stop","run_id":"` + runID + `"}}}}`
	rec := postCard(t, h, body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	// 收尾，避免 goroutine 泄漏。
	engine.Stop("t:c1:ou_owner")
	deadline := time.After(2 * time.Second)
	select {
	case <-done:
	case <-deadline:
		t.Fatal("收尾失败")
	}
}

func TestCardStopRejectsBadToken(t *testing.T) {
	engine, _, done, runID := startRun(t)
	h := feishu.NewCardAction(engine, "vt", "", nil)
	body := `{"header":{"token":"bad"},"event":{"operator":{"open_id":"ou_owner"},"action":{"value":{"action":"stop","run_id":"` + runID + `"}}}}`
	rec := postCard(t, h, body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	engine.Stop("t:c1:ou_owner")
	<-done
}
