package core_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
)

type reactionChannel struct {
	mu     sync.Mutex
	states []string
}

func (c *reactionChannel) SendText(context.Context, channel.Outbound) (channel.Receipt, error) {
	return channel.Receipt{ExternalID: "om_out"}, nil
}

func (c *reactionChannel) React(_ context.Context, _ string, state string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.states = append(c.states, state)
	return "r-" + state, nil
}

func (c *reactionChannel) Unreact(context.Context, string, string) error { return nil }

func TestReactionStatesFollowRun(t *testing.T) {
	ch := &reactionChannel{}
	engine := newEngine(t, ch, agent.Echo{})
	engine.SetReactor(ch)
	if _, err := engine.Submit(context.Background(), inbound("om_react", "你好")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ch.mu.Lock()
		seen := map[string]bool{}
		for _, state := range ch.states {
			seen[state] = true
		}
		ch.mu.Unlock()
		if seen["completed"] && seen["processing"] {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	seen := map[string]bool{}
	for _, state := range ch.states {
		seen[state] = true
	}
	if !seen["completed"] || !seen["processing"] {
		t.Fatalf("states = %+v", ch.states)
	}
}
