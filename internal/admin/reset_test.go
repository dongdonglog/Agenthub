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
)

func TestSessionResetEndpoints(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := core.New(nil, nil, logger)
	convs := []string{"tenant_1:oc_a:u_a", "tenant_1:oc_b:u_b"}
	for i, conv := range convs {
		engine.SetIdentity(conv, "群", "用户")
		if _, err := engine.Submit(context.Background(), channel.Inbound{
			Channel: "feishu", Tenant: "tenant_1", ExternalID: "m" + string(rune('a'+i)),
			Chat:   channel.Chat{ID: conv[9:13], Kind: "group"},
			Sender: channel.Sender{OpenID: "u"}, Text: "hi", SentAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := admin.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	h := admin.New(&config.Config{Storage: config.StorageConfig{Dir: dir}}, engine, st, admin.Options{}, logger).Handler()

	rec := do(t, h, http.MethodPost, "/admin/api/v1/sessions/"+convs[0]+"/reset", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("single status=%d body=%s", rec.Code, rec.Body.String())
	}
	if engine.Generation(convs[0]) != 2 {
		t.Fatalf("generation = %d", engine.Generation(convs[0]))
	}

	rec = do(t, h, http.MethodPost, "/admin/api/v1/sessions/reset", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("batch status=%d body=%s", rec.Code, rec.Body.String())
	}
	if engine.Generation(convs[0]) != 3 {
		t.Fatalf("first generation = %d", engine.Generation(convs[0]))
	}
	if engine.Generation(convs[1]) != 2 {
		t.Fatalf("second generation = %d", engine.Generation(convs[1]))
	}
}
