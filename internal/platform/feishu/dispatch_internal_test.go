package feishu

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/moying/agenthub/internal/agent"
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

type dispatchCaptureChannel struct{ last string }

func (c *dispatchCaptureChannel) SendText(_ context.Context, out channel.Outbound) (channel.Receipt, error) {
	c.last = out.Text
	return channel.Receipt{ExternalID: "om_bot"}, nil
}

func TestAdminQuoteInterventionRoutesToTarget(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ch := &dispatchCaptureChannel{}
	engine := core.New(agent.Echo{}, ch, logger)
	ctx := context.Background()
	if _, err := engine.Submit(ctx, channel.Inbound{
		Channel: "feishu", Tenant: "tenant", ExternalID: "om_user",
		Chat:   channel.Chat{ID: "oc_group", Kind: "group"},
		Sender: channel.Sender{OpenID: "user"}, Text: "第一问", MsgType: "text",
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if ch.last == "" {
		t.Fatal("expected bot reply")
	}

	d := NewDispatcher(engine, Options{
		BotOpenID: "bot", RequireMention: true,
		AllowedUsers: []string{"user"}, AdminOpenIDs: []string{"admin"},
	}, logger)
	payload := &Payload{
		Schema: "2.0",
		Header: Header{EventID: "ev-admin", EventType: "im.message.receive_v1", TenantKey: "tenant"},
	}
	payload.Event.Sender.SenderID.OpenID = "admin"
	payload.Event.Sender.SenderType = "user"
	payload.Event.Message = Message{
		MessageID: "om_admin", ChatID: "oc_group", ChatType: "group", MessageType: "text",
		Content: `{"text":"补充一句"}`, ParentID: "om_bot",
	}
	if err := d.HandleEvent(ctx, payload); err != nil {
		t.Fatal(err)
	}
	target := engine.Messages("tenant:oc_group:user")
	if len(target) != 3 || target[2].Text != "补充一句" || !target[2].SentByAdmin {
		t.Fatalf("target = %+v", target)
	}
	if got := engine.Messages("tenant:oc_group:admin"); len(got) != 0 {
		t.Fatalf("admin conv = %+v", got)
	}
}
