package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
)

type ambiguousOnceChannel struct {
	mu   sync.Mutex
	fail bool
	sent int
}

func (c *ambiguousOnceChannel) SendText(context.Context, channel.Outbound) (channel.Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		c.fail = false
		return channel.Receipt{}, channel.Ambiguous(errors.New("timeout"))
	}
	c.sent++
	return channel.Receipt{ExternalID: "om_retry_ok"}, nil
}

func TestRetryAmbiguousDeliveryFromAdmin(t *testing.T) {
	ch := &ambiguousOnceChannel{fail: true}
	engine := newEngine(t, ch, agent.Echo{})
	ctx := context.Background()
	if _, err := engine.Submit(ctx, inbound("om_manual_retry", "你好")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	items := engine.Deliveries()
	if len(items) != 1 || items[0].State != "ambiguous" {
		t.Fatalf("deliveries = %+v", items)
	}
	if err := engine.RetryDelivery(ctx, items[0].IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		items = engine.Deliveries()
		if len(items) == 1 && items[0].State == "sent" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(items) != 1 || items[0].State != "sent" {
		t.Fatalf("after retry = %+v", items)
	}
}
