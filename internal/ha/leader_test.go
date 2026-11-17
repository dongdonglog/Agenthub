package ha

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moying/agenthub/internal/store"
)

func TestLeaderFailover(t *testing.T) {
	url := os.Getenv("AGENTHUB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("未设置 AGENTHUB_TEST_DATABASE_URL，跳过 HA 用例")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	l := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	first := make(chan struct{})
	second := make(chan struct{})
	ctx1, cancel1 := context.WithCancel(ctx)
	var firstActive atomic.Bool
	go func() {
		_ = l.Run(ctx1, func(context.Context) {
			firstActive.Store(true)
			close(first)
		}, func() { firstActive.Store(false) })
	}()
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("first leader not acquired")
	}

	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	go func() {
		_ = l.Run(ctx2, func(context.Context) { close(second) }, nil)
	}()
	select {
	case <-second:
		t.Fatal("second instance must be standby")
	case <-time.After(500 * time.Millisecond):
	}

	cancel1()
	select {
	case <-second:
	case <-time.After(8 * time.Second):
		t.Fatal("standby did not acquire after leader release")
	}
	if firstActive.Load() {
		t.Fatal("first leader still active after cancel")
	}
}
