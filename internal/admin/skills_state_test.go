package admin_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moying/agenthub/internal/admin"
	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
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

func newServerWithStore(t *testing.T, st *admin.Store) http.Handler {
	t.Helper()
	cfg := &config.Config{Storage: config.StorageConfig{Dir: t.TempDir()}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return admin.New(cfg, core.New(nil, nil, logger), st, admin.Options{}, logger).Handler()
}

func TestSkillFileTree(t *testing.T) {
	dir := t.TempDir()
	st, err := admin.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutSkill("demo", "# demo\n"); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteFile("demo", "references/notes.md", "参考内容"); err != nil {
		t.Fatal(err)
	}
	files, err := st.ListFiles("demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v", files)
	}
	content, err := st.ReadFile("demo", "references/notes.md")
	if err != nil || content != "参考内容" {
		t.Fatalf("read = %q err=%v", content, err)
	}
	if _, err := st.ReadFile("demo", "../escape.md"); err == nil {
		t.Fatal("应拒绝路径穿越")
	}
	if err := st.DeleteFile("demo", "references/notes.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReadFile("demo", "references/notes.md"); err == nil {
		t.Fatal("删除后不应可读")
	}
}

func TestSkillFileAPI(t *testing.T) {
	dir := t.TempDir()
	st, err := admin.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutSkill("demo", "# demo\n"); err != nil {
		t.Fatal(err)
	}
	h := newServerWithStore(t, st)

	rec := do(t, h, http.MethodGet, "/admin/api/v1/skills/demo/files", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "SKILL.md") {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, h, http.MethodPut, "/admin/api/v1/skills/demo/files/references/a.md", `{"content":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put = %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodGet, "/admin/api/v1/skills/demo/files/references/a.md", "")
	if !strings.Contains(rec.Body.String(), `"hi"`) {
		t.Fatalf("get = %s", rec.Body.String())
	}
	rec = do(t, h, http.MethodDelete, "/admin/api/v1/skills/demo/files/references/a.md", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d", rec.Code)
	}
}

func TestProfileApplyEndpoint(t *testing.T) {
	dir := t.TempDir()
	ps := store.NewProfileStore(dir)
	if err := ps.PutProfile(store.Profile{Name: "ops", Skills: []string{"demo"}}); err != nil {
		t.Fatal(err)
	}
	st, err := admin.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Storage: config.StorageConfig{Dir: dir}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := admin.New(cfg, core.New(nil, nil, logger), st, admin.Options{Profiles: ps}, logger).Handler()

	rec := do(t, h, http.MethodPut, "/admin/api/v1/profiles/ops/apply", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("apply = %d %s", rec.Code, rec.Body.String())
	}
	if ps.Active().Name != "ops" {
		t.Fatalf("active = %+v", ps.Active())
	}
	rec = do(t, h, http.MethodGet, "/admin/api/v1/profiles", "")
	if !strings.Contains(rec.Body.String(), `"active":"ops"`) {
		t.Fatalf("profiles = %s", rec.Body.String())
	}
}

func TestSessionProfileRebind(t *testing.T) {
	dir := t.TempDir()
	st, _ := admin.NewStore(dir)
	ps := store.NewProfileStore(dir)
	if err := ps.PutProfile(store.Profile{Name: "ops", Skills: []string{"demo"}}); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := core.New(agent.Echo{}, nil, logger)
	ctx := context.Background()
	in := channel.Inbound{
		Channel: "feishu", Tenant: "t", ExternalID: "e1",
		Chat:   channel.Chat{ID: "c1", Kind: "p2p"},
		Sender: channel.Sender{OpenID: "u1"}, Text: "hi",
	}
	if _, err := engine.Submit(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	conv := "t:c1:u1"
	cfg := &config.Config{Storage: config.StorageConfig{Dir: dir}}
	h := admin.New(cfg, engine, st, admin.Options{Profiles: ps}, logger).Handler()

	rec := do(t, h, http.MethodPut, "/admin/api/v1/sessions/"+conv+"/profile", `{"name":"ops"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rebind = %d %s", rec.Code, rec.Body.String())
	}
	if got := engine.ProfileOf(conv); got != "ops" {
		t.Fatalf("ProfileOf = %s", got)
	}
}

func TestAuditActorFromHeader(t *testing.T) {
	dir := t.TempDir()
	st, _ := admin.NewStore(dir)
	if err := st.PutSkill("demo", "# demo"); err != nil {
		t.Fatal(err)
	}
	var gotActor string
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := admin.New(&config.Config{Storage: config.StorageConfig{Dir: dir}}, core.New(nil, nil, logger), st, admin.Options{
		AuditRecord: func(actor, action, target, detail string) { gotActor = actor },
	}, logger).Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/admin/api/v1/skills/demo", strings.NewReader(`{"content":"# demo"}`))
	req.Header.Set("X-AA-Operator", "alice")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if gotActor != "alice" {
		t.Fatalf("actor = %q", gotActor)
	}
}

func TestHistoryDetailEndpoint(t *testing.T) {
	dir := t.TempDir()
	st, _ := admin.NewStore(dir)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := admin.New(&config.Config{Storage: config.StorageConfig{Dir: dir}}, core.New(nil, nil, logger), st, admin.Options{
		HistoryMessages: func(context.Context, string, int) ([]core.Message, error) {
			return []core.Message{{ID: "m1", Role: "user", Text: "你好"}}, nil
		},
	}, logger).Handler()

	rec := do(t, h, http.MethodGet, "/admin/api/v1/history/t:c1:u1/2", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "你好") {
		t.Fatalf("detail = %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, http.MethodGet, "/admin/api/v1/history/t:c1:u1/abc", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法代 = %d", rec.Code)
	}
}

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSkillImportZip(t *testing.T) {
	dir := t.TempDir()
	st, _ := admin.NewStore(dir)
	payload := buildZip(t, map[string]string{
		"demo/SKILL.md":           "---\nname: demo\ndescription: 演示\n---\n正文",
		"demo/references/note.md": "参考",
	})
	names, err := st.ImportZip(payload, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "demo" {
		t.Fatalf("imported = %v", names)
	}
	if content, err := st.ReadFile("demo", "references/note.md"); err != nil || content != "参考" {
		t.Fatalf("read = %q err=%v", content, err)
	}
	if _, err := st.ImportZip(payload, false); err == nil {
		t.Fatal("同名技能未覆盖时应报错")
	}
	if _, err := st.ImportZip(payload, true); err != nil {
		t.Fatalf("overwrite 应成功: %v", err)
	}
}

func TestSkillImportRejectsZipSlip(t *testing.T) {
	dir := t.TempDir()
	st, _ := admin.NewStore(dir)
	payload := buildZip(t, map[string]string{
		"../evil.md": "x",
	})
	if _, err := st.ImportZip(payload, true); err == nil {
		t.Fatal("应拒绝路径穿越")
	}
}

func TestSkillImportFlatRoot(t *testing.T) {
	dir := t.TempDir()
	st, _ := admin.NewStore(dir)
	payload := buildZip(t, map[string]string{
		"SKILL.md": "---\nname: flat-skill\n---\n正文",
	})
	names, err := st.ImportZip(payload, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "flat-skill" {
		t.Fatalf("imported = %v", names)
	}
}
