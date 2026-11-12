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

// QueueItem 是排队中的会话快照。
type QueueItem struct {
	ConversationID string    `json:"conversation_id"`
	Queued         int       `json:"queued"`
	Priority       int       `json:"priority"`
	Oldest         time.Time `json:"oldest"`
	Effective      int       `json:"effective"`
}

// QueueSnapshot 返回当前排队情况（按调度顺序）。
func (e *Engine) QueueSnapshot() []QueueItem {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.queueItemsLocked(e.now())
}

// queueItemsLocked 计算每个会话的排队与有效优先级；调用方需持有锁。
//
// @remarks 有效优先级 = 基础优先级 + 等待分钟数 × 2，避免低优先级长期饥饿。
func (e *Engine) queueItemsLocked(now time.Time) []QueueItem {
	items := make([]QueueItem, 0, len(e.order))
	for conv, ids := range e.order {
		if len(ids) == 0 || e.active[conv] != "" {
			continue
		}
		oldest := time.Time{}
		for _, id := range ids {
			if m := e.messages[id]; m != nil && (oldest.IsZero() || m.CreatedAt.Before(oldest)) {
				oldest = m.CreatedAt
			}
		}
		priority := e.priority[conv]
		effective := priority
		if !oldest.IsZero() {
			effective += int(now.Sub(oldest).Minutes()) * 2
		}
		items = append(items, QueueItem{
			ConversationID: conv,
			Queued:         len(ids),
			Priority:       priority,
			Oldest:         oldest,
			Effective:      effective,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Effective != items[j].Effective {
			return items[i].Effective > items[j].Effective
		}
		if !items[i].Oldest.Equal(items[j].Oldest) {
			return items[i].Oldest.Before(items[j].Oldest)
		}
		return items[i].ConversationID < items[j].ConversationID
	})
	return items
}

// Dispatch 以并发方式启动各会话的运行，受全局并发上限约束。
//
// @remarks 每个会话仍保持单运行；调度按有效优先级（含等待老化），
// 没有空闲名额时消息留在队列，下一轮再派发。
func (e *Engine) Dispatch(ctx context.Context) {
	e.prune()
	e.mu.Lock()
	items := e.queueItemsLocked(e.now())
	e.mu.Unlock()

	for _, item := range items {
		if !e.acquire() {
			return
		}
		e.mu.Lock()
		if e.active[item.ConversationID] != "" {
			e.mu.Unlock()
			e.release()
			continue
		}
		e.active[item.ConversationID] = "pending"
		conv := item.ConversationID
		e.mu.Unlock()

		go func() {
			defer e.release()
			if err := e.runBatch(ctx, conv); err != nil {
				e.log.Warn("run failed", "conversation", conv, "error", err)
			}
		}()
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
