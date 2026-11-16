package core_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

type persistArtifactAgent struct{ path string }

func (a persistArtifactAgent) Run(_ context.Context, _ agent.Input) (agent.Output, error) {
	return agent.Output{Text: "done", Artifacts: []channel.Artifact{{
		Name: "report.md", Path: a.path, Kind: "file",
	}}}, nil
}

func TestArtifactRowsSurviveRestart(t *testing.T) {
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
	if _, err := db.Pool().Exec(ctx, `TRUNCATE run_artifact, message_state, run_state, delivery_state`); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	src := filepath.Join(root, "report.md")
	if err := os.WriteFile(src, []byte("artifact"), 0o644); err != nil {
		t.Fatal(err)
	}
	journal := store.NewJournal(db)
	engine := core.New(persistArtifactAgent{path: src}, nil, nil)
	engine.SetJournal(journal)
	engine.SetArtifactDir(filepath.Join(root, "artifacts"))
	if _, err := engine.Submit(ctx, channel.Inbound{
		Channel: "feishu", Tenant: "t", ExternalID: "a1",
		Chat:   channel.Chat{ID: "chat-1", Kind: "p2p"},
		Sender: channel.Sender{OpenID: "u1"}, Text: "生成", SentAt: time.Now(),
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
	var count int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM run_artifact WHERE run_id = $1`, runs[0].ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("run_artifact rows = %d", count)
	}

	restored := core.New(nil, nil, nil)
	restored.SetJournal(journal)
	arts := restored.Artifacts(ctx, runs[0].ID)
	if len(arts) != 1 || arts[0].Name != "report.md" || arts[0].Path == "" {
		t.Fatalf("restored artifacts = %+v", arts)
	}
}
