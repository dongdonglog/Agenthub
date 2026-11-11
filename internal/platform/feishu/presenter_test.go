package feishu_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/platform/feishu"
)

func TestCardPresenterLifecycle(t *testing.T) {
	api, srv := newFakeAPI(t)
	client := feishu.NewClient("cli_test", "secret", feishu.WithBaseURL(srv.URL))
	presenter := feishu.NewCardPresenter(client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	handle, err := presenter.Open(ctx, core.Presentation{ChatID: "oc_1", ReplyTo: "om_1", Inputs: 2})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if handle == "" {
		t.Fatal("缺少 message_id")
	}
	if err := presenter.Update(ctx, handle, "正在分析", ""); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := presenter.Close(ctx, handle, "最终结果", false); err != nil {
		t.Fatalf("Close: %v", err)
	}

	sent := api.messages()
	if len(sent) != 3 {
		t.Fatalf("calls = %d, want 3", len(sent))
	}
	if !strings.Contains(sent[0].body["content"].(string), "处理中") {
		t.Fatalf("首帧 = %v", sent[0].body["content"])
	}
	if !strings.Contains(sent[2].body["content"].(string), "处理完成") {
		t.Fatalf("终态 = %v", sent[2].body["content"])
	}
}

func TestCardPresenterLongResultFallsBack(t *testing.T) {
	_, srv := newFakeAPI(t)
	client := feishu.NewClient("cli_test", "secret", feishu.WithBaseURL(srv.URL))
	presenter := feishu.NewCardPresenter(client, nil)

	if err := presenter.Close(context.Background(), "om_x", strings.Repeat("长", 4000), false); err == nil {
		t.Fatal("超长结果应返回错误以便回退文本")
	}
}

func TestCardPresenterStopButtonToggle(t *testing.T) {
	api, srv := newFakeAPI(t)
	client := feishu.NewClient("cli_test", "secret", feishu.WithBaseURL(srv.URL))
	presenter := feishu.NewCardPresenter(client, nil).WithStopButton(true)
	ctx := context.Background()

	handle, err := presenter.Open(ctx, core.Presentation{ChatID: "oc_1", RunID: "run_9", Inputs: 1})
	if err != nil {
		t.Fatal(err)
	}
	first := api.messages()[0].body["content"].(string)
	if !strings.Contains(first, "停止") || !strings.Contains(first, "run_9") {
		t.Fatalf("处理中卡片应带停止按钮: %s", first)
	}
	if err := presenter.Close(ctx, handle, "完成", false); err != nil {
		t.Fatal(err)
	}
	last := api.messages()[len(api.messages())-1].body["content"].(string)
	if strings.Contains(last, "run_9") {
		t.Fatalf("终态卡片应移除按钮: %s", last)
	}
}
