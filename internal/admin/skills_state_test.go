package admin_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/moying/agenthub/internal/admin"
	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

func TestSkillStatusMovesAndLoaderSkips(t *testing.T) {
	dir := t.TempDir()
	store, err := admin.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutSkill("demo", "# demo\n> 描述\n"); err != nil {
		t.Fatal(err)
	}

	skill, err := store.GetSkill("demo")
	if err != nil || skill.Status != "active" {
		t.Fatalf("status = %+v err=%v", skill, err)
	}

	if err := store.SetSkillStatus("demo", "disabled"); err != nil {
		t.Fatal(err)
	}
	skill, err = store.GetSkill("demo")
	if err != nil || skill.Status != "disabled" {
		t.Fatalf("停用后 status = %+v err=%v", skill, err)
	}
	loaded, err := agent.LoadSkills(dir + "/skills")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("停用技能不应被加载: %+v", loaded)
	}

	if err := store.SetSkillStatus("demo", "active"); err != nil {
		t.Fatal(err)
	}
	loaded, _ = agent.LoadSkills(dir + "/skills")
	if len(loaded) != 1 {
		t.Fatalf("启用后应加载 1 个，实际 %d", len(loaded))
	}
}

func storepkgNewProfiles(t *testing.T, dir string) *store.ProfileStore {
	t.Helper()
	return store.NewProfileStore(dir)
}

func storepkgProfile(name string, skills []string) store.Profile {
	return store.Profile{Name: name, Skills: skills}
}

func TestSkillReferencesFromProfiles(t *testing.T) {
	dir := t.TempDir()
	store, err := admin.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutSkill("demo", "# demo\n"); err != nil {
		t.Fatal(err)
	}
	profiles := storepkgNewProfiles(t, dir)
	if err := profiles.PutProfile(storepkgProfile("default", []string{"demo"})); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Storage: config.StorageConfig{Dir: dir}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := admin.New(cfg, core.New(nil, nil, logger), store, admin.Options{Profiles: profiles}, logger).Handler()

	rec := do(t, h, http.MethodGet, "/admin/api/v1/skills", "")
	if !strings.Contains(rec.Body.String(), `"references":1`) {
		t.Fatalf("引用数未统计: %s", rec.Body.String())
	}
}

func TestHistoryAndAuditEndpoints(t *testing.T) {
	dir := t.TempDir()
	st, err := admin.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Storage: config.StorageConfig{Dir: dir}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := admin.New(cfg, core.New(nil, nil, logger), st, admin.Options{
		History: func(context.Context) ([]store.Generation, error) {
			return []store.Generation{{ConversationID: "c1", Generation: 2, Messages: 3}}, nil
		},
		AuditList: func(context.Context, int) ([]store.AuditEntry, error) {
			return []store.AuditEntry{{ID: 1, Action: "skill.save", Target: "demo"}}, nil
		},
	}, logger).Handler()

	for _, path := range []string{"/admin/api/v1/history", "/admin/api/v1/audits"} {
		rec := do(t, h, http.MethodGet, path, "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"items"`) {
			t.Fatalf("%s -> %d %s", path, rec.Code, rec.Body.String())
		}
	}
}
