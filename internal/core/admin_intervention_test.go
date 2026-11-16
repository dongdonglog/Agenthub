package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
)

func TestAdminInterventionTargetsOtherConversation(t *testing.T) {
	ch := &captureChannel{}
	engine := newEngine(t, ch, agent.Echo{})
	target := "tenant_1:oc_group:user1"
	adminConv := "tenant_1:oc_group:admin"
	ok, err := engine.Submit(context.Background(), channel.Inbound{
		Channel: "feishu", Tenant: "tenant_1", ExternalID: "admin-msg",
		Chat:   channel.Chat{ID: "oc_group", Kind: "group"},
		Sender: channel.Sender{OpenID: "admin"},
		Text:   "请补充一下", SentAt: time.Now(),
		AdminIntervention: true, TargetConversation: target,
	})
	if err != nil || !ok {
		t.Fatalf("submit ok=%v err=%v", ok, err)
	}
	msgs := engine.Messages(target)
	if len(msgs) != 1 || msgs[0].Text != "请补充一下" || !msgs[0].SentByAdmin {
		t.Fatalf("target messages = %+v", msgs)
	}
	if got := engine.Messages(adminConv); len(got) != 0 {
		t.Fatalf("admin own conversation should stay empty: %+v", got)
	}
}

func TestResolveConversationByBotMessage(t *testing.T) {
	ch := &captureChannel{}
	engine := newEngine(t, ch, agent.Echo{})
	ctx := context.Background()
	if _, err := engine.Submit(ctx, inbound("om_resolve", "你好")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	items := engine.Deliveries()
	if len(items) != 1 || items[0].ExternalID == "" {
		t.Fatalf("deliveries = %+v", items)
	}
	conv, chat, ok := engine.ResolveConversationForMessage(ctx, items[0].ExternalID)
	if !ok || chat != "oc_chat" || conv != "tenant_1:oc_chat:ou_user" {
		t.Fatalf("conv=%s chat=%s ok=%v", conv, chat, ok)
	}
}
