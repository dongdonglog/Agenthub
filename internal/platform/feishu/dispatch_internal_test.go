package feishu

import (
	"io"
	"log/slog"
	"testing"

	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
)

func TestAdminInterventionSkipsUserAllowlist(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDispatcher(core.New(nil, nil, logger), Options{
		AllowedUsers:   []string{"allowed-user"},
		AdminOpenIDs:   []string{"admin-user"},
		RequireMention: true,
	}, logger)
	msg := channel.Inbound{
		Chat:              channel.Chat{ID: "oc_group", Kind: "group"},
		Sender:            channel.Sender{OpenID: "admin-user"},
		AdminIntervention: true,
	}
	if ok, reason := d.allowed(msg); !ok {
		t.Fatalf("admin intervention should bypass user/mention checks: %s", reason)
	}
	if !d.isAdmin("admin-user") || d.isAdmin("other") {
		t.Fatal("admin set mismatch")
	}
}
