package core_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/moying/agenthub/internal/agent"
	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

// countingAgent 记录每个会话被执行的次数，用来发现重复执行。
type countingAgent struct {
	mu    sync.Mutex
	calls map[string]int
}

func (a *countingAgent) Run(_ context.Context, in agent.Input) (agent.Output, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.calls == nil {
		a.calls = make(map[string]int)
	}
	a.calls[in.ConversationID]++
	return agent.Output{Text: "ok"}, nil
}

func (a *countingAgent) count(conv string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[conv]
}

// TestMultiInstanceDoesNotDoubleExecute 复现多实例重复执行。
//
// @remarks active[conv] 是进程内内存，两个实例连同一个库时互不可见：
// 实例 A 受理的消息落库为 queued，实例 B Restore 会把它读进自己的队列，
// 两边各自 runBatch，同一条消息被执行两次。
// 这是「多实例只保证入口单活」掩盖下的执行层缺陷。
func TestMultiInstanceDoesNotDoubleExecute(t *testing.T) {
	url := testDatabaseURL(t)
	ctx := context.Background()
	db, err := store.Open(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `TRUNCATE message_state, run_state, delivery_state`); err != nil {
		t.Fatal(err)
	}

	journal := store.NewJournal(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	agentA := &countingAgent{}
	agentB := &countingAgent{}
	instA := core.New(agentA, &captureChannel{}, logger)
	instB := core.New(agentB, &captureChannel{}, logger)
	instA.SetJournal(journal)
	instB.SetJournal(journal)

	in := channel.Inbound{
		Channel: "feishu", Tenant: "t", ExternalID: "e-multi",
		Chat:   channel.Chat{ID: "c1", Kind: "p2p"},
		Sender: channel.Sender{OpenID: "u1"}, Text: "只应执行一次",
	}
	// 实例 A 受理：落库为 queued。
	if _, err := instA.Submit(ctx, in); err != nil {
		t.Fatal(err)
	}

	// 实例 B 启动恢复：把这条 queued 消息读进自己的内存队列。
	if err := instB.Restore(ctx); err != nil {
		t.Fatal(err)
	}

	// 两个实例各自调度。
	if err := instA.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := instB.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	total := agentA.count("t:c1:u1") + agentB.count("t:c1:u1")
	if total != 1 {
		t.Fatalf("同一条消息被执行 %d 次（A=%d B=%d），want 1",
			total, agentA.count("t:c1:u1"), agentB.count("t:c1:u1"))
	}
}
