package core_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent/pi"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
)

func TestConcurrentRunsWithRealPi(t *testing.T) {
	key := os.Getenv("PI_E2E_API_KEY")
	if key == "" {
		t.Skip("未设置 PI_E2E_API_KEY，跳过真实 pi 并发用例")
	}
	base := os.Getenv("PI_E2E_BASE_URL")
	if base == "" {
		base = "https://llm-gateway.moyingrobotics.com/v1"
	}
	model := os.Getenv("PI_E2E_MODEL")
	if model == "" {
		model = "my-deepseek-flash"
	}

	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	if err := pi.PrepareAgentDir(pi.AgentConfig{Dir: agentDir, BaseURL: base, APIKey: key, Model: model}); err != nil {
		t.Fatal(err)
	}
	rt := pi.New(pi.Config{
		Bin: "pi", AgentDir: agentDir,
		SessionDir: filepath.Join(root, "sessions"), WorkspaceDir: filepath.Join(root, "workspaces"),
		Provider: "agenthub", Model: model, Isolation: "process_group", Timeout: 120 * time.Second,
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ch := &captureChannel{}
	eng := core.New(rt, ch, logger)
	eng.SetConcurrency(5)

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := eng.Submit(ctx, channel.Inbound{
			Channel: "feishu", Tenant: "e2e", ExternalID: fmt.Sprintf("e2e-%d", i),
			Chat:   channel.Chat{ID: fmt.Sprintf("chat-%d", i), Kind: "p2p"},
			Sender: channel.Sender{OpenID: fmt.Sprintf("user-%d", i)},
			Text:   "只回答两个字：你好",
		}); err != nil {
			t.Fatal(err)
		}
	}
	eng.Dispatch(ctx)
	deadline, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	if !eng.WaitIdle(deadline) {
		t.Fatal("并发运行未在时限内完成")
	}

	runs := eng.Runs()
	if len(runs) != 5 {
		t.Fatalf("runs = %d", len(runs))
	}
	var tokens int
	for _, run := range runs {
		if run.State != "completed" {
			t.Fatalf("run %s state = %s", run.ID, run.State)
		}
		t.Logf("run %s tokens=%d/%d", run.ID, run.InputTokens, run.OutputTokens)
		tokens += run.InputTokens + run.OutputTokens
	}
	if tokens == 0 {
		t.Fatal("未记录 token 用量")
	}
	t.Logf("5 并发完成，tokens 合计 %d", tokens)
}
