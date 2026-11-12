package core

import (
	"context"
	"sort"
	"time"
)

// allowLocked 检查并记录会话消息频率；调用方需持有锁。
func (e *Engine) allowLocked(conv string, now time.Time) bool {
	window := e.rateWindow
	if window <= 0 {
		window = time.Minute
	}
	cutoff := now.Add(-window)
	kept := e.hits[conv][:0]
	for _, t := range e.hits[conv] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= e.rateLimit {
		e.hits[conv] = kept
		return false
	}
	e.hits[conv] = append(kept, now)
	return true
}

// Dispatch 以并发方式启动各会话的运行，受全局并发上限约束。
//
// @remarks 每个会话仍保持单运行；没有空闲名额时消息留在队列，下一轮再派发。
func (e *Engine) Dispatch(ctx context.Context) {
	e.prune()
	e.mu.Lock()
	candidates := make([]string, 0, len(e.order))
	for conv, ids := range e.order {
		if len(ids) > 0 && e.active[conv] == "" {
			candidates = append(candidates, conv)
		}
	}
	e.mu.Unlock()
	sort.Strings(candidates)

	for _, conv := range candidates {
		if !e.acquire() {
			return
		}
		e.mu.Lock()
		if e.active[conv] != "" {
			e.mu.Unlock()
			e.release()
			continue
		}
		e.active[conv] = "pending"
		e.mu.Unlock()

		go func(conv string) {
			defer e.release()
			if err := e.runBatch(ctx, conv); err != nil {
				e.log.Warn("run failed", "conversation", conv, "error", err)
			}
		}(conv)
	}
}

func (e *Engine) acquire() bool {
	if e.runSem == nil {
		return true
	}
	select {
	case e.runSem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (e *Engine) release() {
	if e.runSem != nil {
		<-e.runSem
	}
}
