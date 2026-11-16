package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/agent"
)

func TestExpiredQueueIsSkippedWithNotice(t *testing.T) {
	base := time.Unix(1700000000, 0)
	ch := &captureChannel{}
	engine := newEngine(t, ch, agentEcho{})
	engine.SetQueueTTL(time.Hour)
	engine.SetNow(func() time.Time { return base })

	if _, err := engine.Submit(context.Background(), inbound("om_ttl_1", "旧消息")); err != nil {
		t.Fatal(err)
	}
	engine.SetNow(func() time.Time { return base.Add(2 * time.Hour) })
	if err := engine.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runs := engine.Runs(); len(runs) != 0 {
		t.Fatalf("过期消息不应产生运行: %+v", runs)
	}
	if ch.count() != 1 || !strings.Contains(ch.sent[0].Text, "等待超过 1 小时") {
		t.Fatalf("notice = %+v", ch.sent)
	}
	msgs := engine.Messages("tenant_1:oc_chat:ou_user")
	if len(msgs) != 1 || msgs[0].Status != "expired" {
		t.Fatalf("messages = %+v", msgs)
	}
}

func TestMixedQueueKeepsFreshMessages(t *testing.T) {
	base := time.Unix(1700000000, 0)
	ch := &captureChannel{}
	engine := newEngine(t, ch, agentEcho{})
	engine.SetQueueTTL(time.Hour)
	engine.SetNow(func() time.Time { return base })
	if _, err := engine.Submit(context.Background(), inbound("om_ttl_old", "旧消息")); err != nil {
		t.Fatal(err)
	}
	engine.SetNow(func() time.Time { return base.Add(2 * time.Hour) })
	if _, err := engine.Submit(context.Background(), inbound("om_ttl_new", "新消息")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs := engine.Runs()
	if len(runs) != 1 || len(runs[0].InputIDs) != 1 {
		t.Fatalf("runs = %+v", runs)
	}
	if !strings.Contains(ch.sent[0].Text, "等待超过 1 小时") {
		t.Fatalf("result = %s", ch.sent[0].Text)
	}
}

// agentEcho 是 agent.Echo 的轻量别名，保持测试可读。
type agentEcho = agent.Echo
