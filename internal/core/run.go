package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/agent"
)

func (e *Engine) runBatch(ctx context.Context, conv string) error {
	e.mu.Lock()
	if e.active[conv] != "" {
		e.mu.Unlock()
		return nil
	}
	batch := make([]*Message, 0, len(e.order[conv]))
	for _, id := range e.order[conv] {
		if m := e.messages[id]; m != nil && m.Status == "queued" {
			batch = append(batch, m)
		}
	}
	if len(batch) == 0 {
		e.order[conv] = nil
		e.mu.Unlock()
		return nil
	}
	// 批次快照：清空队列并冻结输入（不变量 I2）。
	e.order[conv] = nil
	e.runSeq++
	run := &Run{
		TraceID:        batch[0].TraceID,
		ID:             fmt.Sprintf("run_%d", e.runSeq),
		ConversationID: conv,
		State:          "running",
		StartedAt:      e.now(),
	}
	for _, m := range batch {
		m.Status = "processing"
		run.InputIDs = append(run.InputIDs, m.ID)
	}
	e.runs[run.ID] = run
	e.runOrder = append(e.runOrder, run.ID)
	e.active[conv] = run.ID

	runCtx, cancel := context.WithCancel(ctx)
	e.cancels[conv] = cancel
	e.mu.Unlock()

	if e.journal != nil {
		if err := e.journal.SaveRun(ctx, run); err != nil {
			e.log.Error("persist run failed", "error", err, "run", run.ID)
		}
		if err := e.journal.SetMessageStatus(ctx, run.InputIDs, "processing"); err != nil {
			e.log.Error("persist message status failed", "error", err)
		}
	}

	var cardHandle string
	if e.presenter != nil {
		handle, err := e.presenter.Open(ctx, Presentation{
			ConversationID: conv,
			ChatID:         batch[0].ChatID,
			ReplyTo:        batch[0].ExternalID,
			RunID:          run.ID,
			Inputs:         len(batch),
		})
		if err != nil {
			e.log.Warn("presenter open failed; fallback to text", "error", err, "run", run.ID)
		} else {
			cardHandle = handle
		}
	}

	lastProgress := time.Time{}
	progress := func(phase string) {
		e.recordEvent(run.ID, RunEvent{Kind: "phase", Name: phase})
		if cardHandle == "" || e.presenter == nil || phase == "" {
			return
		}
		if time.Since(lastProgress) < 800*time.Millisecond {
			return
		}
		lastProgress = time.Now()
		if err := e.presenter.Update(ctx, cardHandle, phase, ""); err != nil {
			e.log.Debug("presenter update failed", "error", err)
		}
	}

	e.log.Info("run started", "run", run.ID, "trace", run.TraceID, "inputs", len(batch))
	input := agent.Input{
		TraceID:        run.TraceID,
		RunID:          run.ID,
		ConversationID: conv,
		Progress:       progress,
		History:        e.history(conv, batch),
		OnTool: func(name string, dur time.Duration, err error) {
			detail := "ok"
			if err != nil {
				detail = err.Error()
			}
			e.recordEvent(run.ID, RunEvent{Kind: "tool", Name: name, Detail: detail, DurationMS: dur.Milliseconds()})
		},
	}
	for _, m := range batch {
		input.Messages = append(input.Messages, agent.Message{ID: m.ID, Text: m.Text, Attachments: m.Attachments})
	}

	out, err := e.agent.Run(runCtx, input)
	cancel()

	e.mu.Lock()
	run.EndedAt = e.now()
	run.InputTokens = out.Usage.InputTokens
	run.OutputTokens = out.Usage.OutputTokens
	text := out.Text
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		run.State = "interrupted"
		run.Error = err.Error()
		if text == "" {
			text = "已停止当前处理。"
		}
		for _, m := range batch {
			m.Status = "stopped"
		}
	case err != nil:
		run.State = "failed"
		run.Error = err.Error()
		if text == "" {
			text = "处理失败：" + err.Error()
		}
		for _, m := range batch {
			m.Status = "failed"
		}
	default:
		run.State = "completed"
		run.Output = text
		for _, m := range batch {
			m.Status = "done"
		}
	}
	delete(e.active, conv)
	delete(e.cancels, conv)
	e.log.Info("run finished", "run", run.ID, "trace", run.TraceID, "state", run.State)
	cardDone := false
	if cardHandle != "" && e.presenter != nil {
		if err := e.presenter.Close(ctx, cardHandle, text, run.State != "completed"); err != nil {
			e.log.Warn("presenter close failed; fallback to text", "error", err, "run", run.ID)
		} else {
			cardDone = true
		}
	}

	delivery := &Delivery{
		TraceID:        run.TraceID,
		CreatedAt:      e.now(),
		RunID:          run.ID,
		IdempotencyKey: run.ID + ":result",
		ChatID:         batch[0].ChatID,
		ReplyTo:        batch[0].ExternalID,
		Text:           text,
		State:          "pending",
	}
	if cardDone {
		delivery.State = "sent"
		delivery.ExternalID = cardHandle
	}
	e.deliveries[delivery.IdempotencyKey] = delivery
	alreadySent := e.sent[delivery.IdempotencyKey]
	e.mu.Unlock()

	e.appendAssistant(conv, run.ID, text)
	if e.metrics != nil {
		e.metrics.Inc("agenthub_runs_total", map[string]string{"state": run.State})
	}

	if e.journal != nil {
		if err := e.journal.SaveRun(ctx, run); err != nil {
			e.log.Error("persist run failed", "error", err, "run", run.ID)
		}
		if err := e.journal.SetMessageStatus(ctx, run.InputIDs, batch[0].Status); err != nil {
			e.log.Error("persist message status failed", "error", err)
		}
		if err := e.journal.SaveDelivery(ctx, delivery); err != nil {
			e.log.Error("persist delivery failed", "error", err, "delivery", delivery.IdempotencyKey)
		}
	}

	if alreadySent || cardDone {
		return nil
	}
	e.send(ctx, delivery)
	return nil
}

// history 返回本批之前的最近对话，用于多轮上下文。

func (e *Engine) history(conv string, batch []*Message) []agent.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	skip := make(map[string]struct{}, len(batch))
	for _, m := range batch {
		skip[m.ID] = struct{}{}
	}
	msgs := make([]*Message, 0, len(e.messages))
	for _, m := range e.messages {
		if m.ConversationID != conv {
			continue
		}
		if _, ok := skip[m.ID]; ok {
			continue
		}
		if m.Status != "done" && m.Status != "failed" && m.Status != "stopped" {
			continue
		}
		if m.Generation != e.generationLocked(conv) {
			continue
		}
		msgs = append(msgs, m)
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].CreatedAt.Before(msgs[j].CreatedAt) })
	if len(msgs) > 20 {
		msgs = msgs[len(msgs)-20:]
	}
	out := make([]agent.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, agent.Message{ID: m.ID, Text: m.Text, Attachments: m.Attachments, Role: m.Role})
	}
	return out
}

// appendAssistant 记录助手回复，供下一轮作为上下文。

func (e *Engine) appendAssistant(conv, runID, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	msg := &Message{
		ID:             "asst_" + runID,
		Role:           "assistant",
		Generation:     e.Generation(conv),
		ConversationID: conv,
		ChatID:         "",
		Status:         "done",
		Text:           text,
		CreatedAt:      e.now(),
	}
	e.mu.Lock()
	e.messages[msg.ID] = msg
	e.mu.Unlock()
	if e.journal != nil {
		if _, err := e.journal.SaveMessage(context.Background(), msg); err != nil {
			e.log.Error("persist assistant message failed", "error", err)
		}
	}
}

// maxDeliveryAttempts 是投递重试上限。
const maxDeliveryAttempts = 3
