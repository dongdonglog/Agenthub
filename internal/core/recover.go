package core

import (
	"context"
	"fmt"
	"time"
)

// RecoverStaleRuns 回收失去执行者的运行，并为每个回收的运行投递一条中断说明。
//
// @remarks 进程被 kill 或崩溃后，数据库里 state=running 的行没有任何执行者，
// 卡片会永远停在「处理中」。这里按 staleRunAfter 判定，把它们标成 interrupted，
// 同时补一条说明投递（不变量：中断必须有结果说明）。
// 本进程内存中 active 的会话会被排除，不会误伤正在执行的任务。
//
// @returns 本次回收的运行数
func (e *Engine) RecoverStaleRuns(ctx context.Context) int {
	if e.journal == nil {
		return 0
	}
	e.mu.Lock()
	active := make([]string, 0, len(e.active))
	for conv, runID := range e.active {
		if runID != "" && runID != "pending" {
			active = append(active, conv)
		}
	}
	staleAfter := e.staleRunAfter
	e.mu.Unlock()
	if staleAfter <= 0 {
		staleAfter = 30 * time.Minute
	}

	now := e.now()
	runs, err := e.journal.RecoverStaleRuns(ctx, now.Add(-staleAfter), now, active)
	if err != nil {
		e.log.Warn("recover stale runs failed", "error", err)
		return 0
	}
	for i, run := range runs {
		e.mu.Lock()
		if existing := e.runs[run.ID]; existing != nil {
			existing.State = run.State
			existing.EndedAt = run.EndedAt
			existing.Error = run.Error
		}
		e.mu.Unlock()
		e.log.Warn("stale run recovered as interrupted", "run", run.ID, "conversation", run.ConversationID)
		if err := e.journal.SetMessageStatus(ctx, run.InputIDs, "stopped"); err != nil {
			e.log.Warn("mark stale run inputs failed", "run", run.ID, "error", err)
		}
		// 只给最近恢复的前 10 条发说明，避免大批量重启时刷屏。
		if run.ChatID == "" || i >= 10 {
			continue
		}
		e.notifyInterrupted(ctx, run)
	}
	return len(runs)
}

// notifyInterrupted 为回收的运行补一条说明投递。
func (e *Engine) notifyInterrupted(ctx context.Context, run *Run) {
	delivery := &Delivery{
		TraceID:        run.TraceID,
		CreatedAt:      e.now(),
		RunID:          run.ID,
		IdempotencyKey: "interrupted:" + run.ID,
		ChatID:         run.ChatID,
		Text:           fmt.Sprintf("这次处理因进程中断已结束（%s），没有产出结果。请重新发送。", run.ID),
		State:          "pending",
	}
	e.mu.Lock()
	if _, exists := e.deliveries[delivery.IdempotencyKey]; exists {
		e.mu.Unlock()
		return
	}
	e.deliveries[delivery.IdempotencyKey] = delivery
	e.mu.Unlock()
	if err := e.journal.SaveDelivery(ctx, delivery); err != nil {
		e.log.Warn("persist interrupted notice failed", "run", run.ID, "error", err)
		return
	}
	e.send(ctx, delivery)
}
