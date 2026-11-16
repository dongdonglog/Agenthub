package admin_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/admin"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/config"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

func TestApplyConfigToExistingSessions(t *testing.T) {
	dir := t.TempDir()
	ps := store.NewProfileStore(dir)
	if err := ps.PutProfile(store.Profile{Name: "ops", Skills: []string{"demo"}, MCPs: []string{"mcp"}}); err != nil {
		t.Fatal(err)
	}
	if err := ps.Apply("ops"); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := core.New(nil, nil, logger)
	conv := "tenant_1:oc_chat:ou_user"
	engine.SetIdentity(conv, "测试群", "测试用户")
	if _, err := engine.Submit(context.Background(), channel.Inbound{
		Channel: "feishu", Tenant: "tenant_1", ExternalID: "m1",
		Chat:   channel.Chat{ID: "oc_chat", Kind: "p2p"},
		Sender: channel.Sender{OpenID: "ou_user"},
		Text:   "你好", SentAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	reloads := 0
	st, err := admin.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := admin.New(&config.Config{Storage: config.StorageConfig{Dir: dir}}, engine, st, admin.Options{
		Profiles: ps,
		ReloadAgentConfig: func() error {
			reloads++
			return nil
		},
	}, logger).Handler()

	rec := do(t, h, http.MethodPost, "/admin/api/v1/sessions/apply-config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if reloads != 1 {
		t.Fatalf("reloads = %d", reloads)
	}
	if got := engine.ProfileOf(conv); got != "ops" {
		t.Fatalf("profile = %q", got)
	}

	rec = do(t, h, http.MethodPost, "/admin/api/v1/sessions/"+conv+"/apply-config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("single status=%d body=%s", rec.Code, rec.Body.String())
	}
}
