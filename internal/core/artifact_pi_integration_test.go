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

func TestArtifactDeliveryWithRealPi(t *testing.T) {
	key := os.Getenv("PI_E2E_API_KEY")
	if key == "" {
		t.Skip("未设置 PI_E2E_API_KEY，跳过真实 pi 产物用例")
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
	ch := &artifactChannel{}
	eng := core.New(rt, ch, slog.New(slog.NewTextHandler(io.Discard, nil)))
	eng.SetArtifactDir(filepath.Join(root, "artifacts"))

	ctx := context.Background()
	if _, err := eng.Submit(ctx, channel.Inbound{
		Channel: "feishu", Tenant: "e2e", ExternalID: "artifact-e2e",
		Chat:   channel.Chat{ID: "chat-artifact", Kind: "p2p"},
		Sender: channel.Sender{OpenID: "user-artifact"},
		Text:   "请在当前工作区的 submit/ 目录创建 hello.txt，内容只写 hello，然后只回复 OK。",
	}); err != nil {
		t.Fatal(err)
	}
	if err := eng.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	runs := eng.Runs()
	runID := ""
	if len(runs) == 1 {
		runID = runs[0].ID
	}
	arts := eng.Artifacts(ctx, runID)
	if len(arts) != 1 || arts[0].Name != "hello.txt" {
		t.Fatalf("artifacts = %+v", arts)
	}
	body, err := os.ReadFile(arts[0].Path)
	if err != nil || string(body) != "hello" {
		t.Fatalf("body = %q err=%v", body, err)
	}
	if len(ch.artifacts) != 1 {
		t.Fatalf("delivered = %+v", ch.artifacts)
	}
	fmt.Println("real pi artifact delivered:", ch.artifacts[0].Artifact.Name)
}
