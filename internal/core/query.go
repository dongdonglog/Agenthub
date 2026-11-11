package core

import (
	"context"
	"sort"
	"time"
)

func (e *Engine) recordEvent(runID string, ev RunEvent) {
	if runID == "" {
		return
	}
	ev.RunID = runID
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = e.now()
	}
	e.mu.Lock()
	list := append(e.events[runID], ev)
	if len(list) > 100 {
		list = list[len(list)-100:]
	}
	e.events[runID] = list
	e.mu.Unlock()

	if e.journal != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := e.journal.SaveRunEvent(ctx, ev); err != nil {
			e.log.Debug("persist run event failed", "error", err)
		}
	}
}

func (e *Engine) RunEvents(runID string) []RunEvent {
	e.mu.Lock()
	cached := e.events[runID]
	e.mu.Unlock()
	if len(cached) == 0 && e.journal != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if loaded, err := e.journal.LoadRunEvents(ctx, runID); err == nil && len(loaded) > 0 {
			e.mu.Lock()
			e.events[runID] = loaded
			e.mu.Unlock()
			cached = loaded
		}
	}
	out := make([]RunEvent, len(cached))
	copy(out, cached)
	return out
}

func (e *Engine) RunByID(runID string) *Run {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.runs[runID]
	if r == nil {
		return nil
	}
	c := *r
	return &c
}

func (e *Engine) DeliveriesFor(runID string) []*Delivery {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []*Delivery
	for _, d := range e.deliveries {
		if d.RunID == runID {
			c := *d
			out = append(out, &c)
		}
	}
	return out
}

func (e *Engine) Messages(conv string) []*Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Message, 0, len(e.messages))
	for _, m := range e.messages {
		if m.ConversationID == conv {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	copies := make([]*Message, len(out))
	for i, m := range out {
		c := *m
		copies[i] = &c
	}
	return copies
}

func (e *Engine) Conversations() []Conversation {
	e.mu.Lock()
	defer e.mu.Unlock()
	index := make(map[string]*Conversation)
	for _, m := range e.messages {
		c, ok := index[m.ConversationID]
		if !ok {
			c = &Conversation{ID: m.ConversationID, ChatID: m.ChatID, Sender: m.SenderOpenID}
			index[m.ConversationID] = c
		}
		c.Messages++
		if m.Status == "queued" {
			c.Queued++
		}
		if m.CreatedAt.After(c.LastActive) {
			c.LastActive = m.CreatedAt
		}
	}
	for conv, runID := range e.active {
		if c, ok := index[conv]; ok {
			c.ActiveRunID = runID
		}
	}
	out := make([]Conversation, 0, len(index))
	for _, c := range index {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive.After(out[j].LastActive) })
	return out
}

func (e *Engine) Runs() []*Run {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Run, 0, len(e.runOrder))
	for _, id := range e.runOrder {
		if r := e.runs[id]; r != nil {
			c := *r
			out = append(out, &c)
		}
	}
	return out
}

func (e *Engine) Deliveries() []*Delivery {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Delivery, 0, len(e.deliveries))
	for _, d := range e.deliveries {
		c := *d
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IdempotencyKey < out[j].IdempotencyKey })
	return out
}
