package core

import (
	"context"
	"strings"
	"time"
)

func (e *Engine) Restore(ctx context.Context) error {
	if e.journal == nil {
		return nil
	}
	msgs, runs, err := e.journal.LoadRecent(ctx, 1000)
	if err != nil {
		return err
	}
	e.mu.Lock()
	for _, m := range msgs {
		if m.Status == "processing" {
			m.Status = "queued"
		}
		if m.Generation > e.generations[m.ConversationID] {
			e.generations[m.ConversationID] = m.Generation
		}
		e.messages[m.ID] = m
		e.dedupe[m.ConversationID+"|"+m.ExternalID] = m.ID
		if n := seqSuffix(m.ID); n > e.seq[m.ConversationID] {
			e.seq[m.ConversationID] = n
		}
		if m.Status == "queued" {
			e.order[m.ConversationID] = append(e.order[m.ConversationID], m.ID)
		}
	}
	if gens, err := e.journal.LoadGenerations(ctx); err == nil {
		for conv, gen := range gens {
			if gen > e.generations[conv] {
				e.generations[conv] = gen
			}
		}
	}
	for _, r := range runs {
		e.runs[r.ID] = r
		e.runOrder = append(e.runOrder, r.ID)
		if n := seqSuffix(r.ID); n > e.runSeq {
			e.runSeq = n
		}
	}
	e.mu.Unlock()

	if bindings, err := e.journal.LoadProfileBindings(ctx, 1000); err == nil {
		for key, p := range bindings {
			e.profiles[key] = p
		}
	}

	// 启动补偿：上次未送达的结果继续投递。
	if pending, err := e.journal.LoadPendingDeliveries(ctx, 100); err == nil {
		for _, d := range pending {
			e.mu.Lock()
			e.deliveries[d.IdempotencyKey] = d
			e.mu.Unlock()
			delay := time.Until(d.NextAttempt)
			if delay < 0 {
				delay = 0
			}
			e.log.Info("resuming pending delivery", "delivery", d.IdempotencyKey, "delay", delay)
			go func(item *Delivery) {
				if delay > 0 {
					select {
					case <-ctx.Done():
						return
					case <-time.After(delay):
					}
				}
				e.send(ctx, item)
			}(d)
		}
	}
	return nil
}

func seqSuffix(id string) int64 {
	i := strings.LastIndex(id, "_")
	if i < 0 || i == len(id)-1 {
		return 0
	}
	var n int64
	for _, c := range id[i+1:] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

// Submit 受理一条消息。
//
// @returns inserted 为 false 表示重复投递，已忽略
// @throws 缺少外部消息 ID 或 chat_id 时返回错误
