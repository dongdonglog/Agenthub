package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
)

func TestGroupRoundNumberIsSharedWithinChat(t *testing.T) {
	ch := &captureChannel{}
	engine := newEngine(t, ch, agent.Echo{})
	ctx := context.Background()
	for i, user := range []string{"u1", "u2"} {
		if _, err := engine.Submit(ctx, channel.Inbound{
			Channel: "feishu", Tenant: "t", ExternalID: "m" + user,
			Chat:   channel.Chat{ID: "oc_group", Kind: "group"},
			Sender: channel.Sender{OpenID: user}, Text: "hi", SentAt: time.Now().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	runs := engine.Runs()
	if len(runs) != 2 {
		t.Fatalf("runs = %d", len(runs))
	}
	seen := map[int]bool{}
	for _, run := range runs {
		if run.ChatID != "oc_group" {
			t.Fatalf("chat = %s", run.ChatID)
		}
		seen[run.Round] = true
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("rounds = %+v", seen)
	}
}
