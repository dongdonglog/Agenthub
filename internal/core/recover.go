package core

import (
	"context"
	"time"
)

// heartbeatInterval 是运行心跳间隔。
const heartbeatInterval = 10 * time.Second

// runHeartbeatTimeout 是判定执行进程已消失的心跳超时。
//
// @remarks 取心跳间隔的 6 倍，容忍几次写库抖动；超过该时长没有心跳即回收，
// 保证卡片最迟在启动后 1 分钟内被改成中断态，不会一直显示「处理中」。
const runHeartbeatTimeout = time.Minute

// startRunHeartbeat 在运行期间定期刷新心跳，返回停止函数。
//
// @remarks 心跳是「进程还活着」的唯一信号。进程被 kill 后心跳停止，
// 其它实例据此在 runHeartbeatTimeout 内回收该运行并把卡片改成中断态。
func (e *Engine) startRunHeartbeat(ctx context.Context, runID string) func() {
	if e.journal == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				hbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := e.journal.HeartbeatRun(hbCtx, runID, e.now()); err != nil {
					e.log.Debug("run heartbeat failed", "run", runID, "error", err)
				}
				cancel()
			}
		}
	}()
	var once bool
	return func() {
		if once {
			return
		}
		once = true
		close(done)
	}
}

// RecoverStaleRuns 回收心跳已停止的运行，并把它们的卡片改成中断态。
//
// @remarks 进程被 kill 或崩溃后，数据库里 state=running 的行没有任何执行者。
// 这里按心跳判定（而不是运行时长），所以真正在跑的长任务不会被误伤。
// 回收时优先更新已持久化的卡片句柄，避免留下永远停在「处理中」的卡片。
// 本进程内存中 active 的会话会被排除，作为心跳写入失败时的兜底。
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
		staleAfter = runHeartbeatTimeout
	}

	now := e.now()
	runs, err := e.journal.RecoverStaleRuns(ctx, now.Add(-staleAfter), now, active)
	if err != nil {
		e.log.Warn("recover stale runs failed", "error", err)
		return 0
	}
	for _, run := range runs {
		e.mu.Lock()
		if existing := e.runs[run.ID]; existing != nil {
			existing.State = run.State
			existing.EndedAt = run.EndedAt
			existing.Error = run.Error
		}
		delete(e.active, run.ConversationID)
		delete(e.cancels, run.ConversationID)
		e.mu.Unlock()
		e.log.Warn("stale run recovered as interrupted", "run", run.ID, "conversation", run.ConversationID)
		if err := e.journal.SetMessageStatus(ctx, run.InputIDs, "stopped"); err != nil {
			e.log.Warn("mark stale run inputs failed", "run", run.ID, "error", err)
		}
		const body = "处理进程已中断，本次没有产出结果。请重新发送。"
		// 先改卡片：卡片是用户唯一看到的东西，绝不能停在「处理中」。
		if run.CardHandle != "" && e.presenter != nil {
			if err := e.presenter.Close(ctx, run.CardHandle, body, true); err == nil {
				continue
			} else {
				e.log.Warn("close stale card failed, fallback to text", "run", run.ID, "error", err)
			}
		}
		if run.ChatID != "" {
			e.notifyInterrupted(ctx, run, body)
		}
	}
	return len(runs)
}

// maxCardCloseAttempts 是卡片结算的重试上限。
//
// @remarks 超过后不再重试，改为记错误日志：说明平台接口侧持续失败，
// 继续重试没有意义。
const maxCardCloseAttempts = 20

// SweepUnclosedCards 为「已结束但卡片没收尾」的运行补上终态。
//
// @remarks 覆盖两类缺口：
//   - 运行正常结束，但 presenter.Close 因网络/限流失败，卡片停在「处理中」；
//   - 进程崩溃被回收后，改卡片那一步同样可能失败。
//
// 卡片是用户唯一能看到的状态，所以即使运行已经结束，也要持续把它推进到终态。
//
// @returns 本次成功收尾的卡片数
func (e *Engine) SweepUnclosedCards(ctx context.Context) int {
	if e.journal == nil || e.presenter == nil {
		return 0
	}
	runs, err := e.journal.LoadUnclosedCards(ctx, 50)
	if err != nil {
		e.log.Warn("load unclosed cards failed", "error", err)
		return 0
	}
	closed := 0
	for _, run := range runs {
		body := run.Output
		failed := run.State != "completed"
		if failed {
			body = "处理进程已中断，本次没有产出结果。请重新发送。"
			if run.Error != "" {
				body = "处理失败：" + run.Error
			}
		}
		closeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := e.presenter.Close(closeCtx, run.CardHandle, body, failed)
		cancel()
		if err == nil {
			if err := e.journal.MarkCardClosed(ctx, run.ID); err != nil {
				e.log.Warn("mark card closed failed", "run", run.ID, "error", err)
			}
			closed++
			e.log.Info("unclosed card settled", "run", run.ID, "state", run.State)
			continue
		}
		attempts, bumpErr := e.journal.BumpCardCloseAttempt(ctx, run.ID)
		if bumpErr != nil {
			e.log.Warn("bump card close attempt failed", "run", run.ID, "error", bumpErr)
		}
		if attempts >= maxCardCloseAttempts {
			e.log.Error("card close gave up", "run", run.ID, "attempts", attempts, "error", err)
			// 放弃前尽量让用户知道：卡片改不动时至少发一条文本。
			if run.ChatID != "" {
				e.notifyInterrupted(ctx, run, body)
			}
			continue
		}
		e.log.Warn("unclosed card retry failed", "run", run.ID, "attempts", attempts, "error", err)
	}
	return closed
}
func (e *Engine) notifyInterrupted(ctx context.Context, run *Run, body string) {
	delivery := &Delivery{
		TraceID:        run.TraceID,
		CreatedAt:      e.now(),
		RunID:          run.ID,
		IdempotencyKey: "interrupted:" + run.ID,
		ChatID:         run.ChatID,
		ReplyTo:        "",
		Text:           body,
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
