package admin_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/admin"
	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/core"
)

type adminArtifactAgent struct{ path string }

func (a adminArtifactAgent) Run(_ context.Context, _ agent.Input) (agent.Output, error) {
	return agent.Output{
		Text: "完成",
		Artifacts: []channel.Artifact{{
			Name: "报告.md", Path: a.path, Kind: "file",
		}},
	}, nil
}

func TestRunDetailAndArtifactDownload(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "报告.md")
	if err := os.WriteFile(src, []byte("产物内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := core.New(adminArtifactAgent{path: src}, nil, logger)
	engine.SetArtifactDir(filepath.Join(root, "artifacts"))

	ctx := context.Background()
	if _, err := engine.Submit(ctx, channel.Inbound{
		Channel: "feishu", Tenant: "t", ExternalID: "m1",
		Chat:   channel.Chat{ID: "oc_1", Kind: "p2p"},
		Sender: channel.Sender{OpenID: "u1"},
		Text:   "生成报告", SentAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	runs := engine.Runs()
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	arts := engine.Artifacts(ctx, runs[0].ID)
	if len(arts) != 1 {
		t.Fatalf("artifacts = %+v", arts)
	}

	dir := t.TempDir()
	cfg := &config.Config{Storage: config.StorageConfig{Dir: dir}}
	store, err := admin.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	handler := admin.New(cfg, engine, store, admin.Options{}, logger).Handler()

	rec := do(t, handler, http.MethodGet, "/admin/api/v1/runs/"+runs[0].ID, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "报告.md") {
		t.Fatalf("run detail status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = do(t, handler, http.MethodGet, "/admin/api/v1/artifacts/"+arts[0].ID, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "产物内容" {
		t.Fatalf("download status=%d body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("headers = %+v", rec.Header())
	}
}
