package core

import (
	"context"
	"strings"
	"time"
)

func (e *Engine) WaitIdle(ctx context.Context) bool {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		e.mu.Lock()
		active := len(e.active)
		e.mu.Unlock()
		if active == 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// Prune 立即按内存上限裁剪历史数据。
//
// @remarks 只裁剪内存，数据库仍是完整事实源；排队与处理中消息不会被删除。

func (e *Engine) Stop(conv string) bool {
	e.mu.Lock()
	runID := e.active[conv]
	cancel := e.cancels[conv]
	e.mu.Unlock()
	if runID == "" || cancel == nil {
		return false
	}
	cancel()
	return true
}

// Generation 返回会话当前代。

func (e *Engine) Generation(conv string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.generationLocked(conv)
}

func (e *Engine) generation(conv string) int { return e.generationLocked(conv) }

func (e *Engine) generationLocked(conv string) int {
	if g := e.generations[conv]; g > 0 {
		return g
	}
	return 1
}

func (e *Engine) RunOwner(runID string) string {
	run := e.RunByID(runID)
	if run == nil {
		return ""
	}
	parts := strings.Split(run.ConversationID, ":")
	return parts[len(parts)-1]
}

// StopRun 停止指定运行所属会话的当前批次。

func (e *Engine) StopRun(runID string) bool {
	run := e.RunByID(runID)
	if run == nil {
		return false
	}
	return e.Stop(run.ConversationID)
}

// RunByID 返回指定运行。

func (e *Engine) Status(conv string) (queued int, runID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, id := range e.order[conv] {
		if m := e.messages[id]; m != nil && m.Status == "queued" {
			queued++
		}
	}
	return queued, e.active[conv]
}

// Clear 取消会话全部排队消息，返回取消数量。

func (e *Engine) Clear(conv string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	cancelled := 0
	for _, id := range e.order[conv] {
		if m := e.messages[id]; m != nil && m.Status == "queued" {
			m.Status = "cancelled"
			cancelled++
		}
	}
	e.order[conv] = nil
	return cancelled
}

// Reset 停止当前运行、清空排队并开启新会话代。
//
// @remarks 新代不再携带旧历史；旧消息仍保留在库中供追溯。

func (e *Engine) Reset(conv string) bool {
	stopped := e.Stop(conv)
	e.Clear(conv)
	e.mu.Lock()
	e.generations[conv] = e.generationLocked(conv) + 1
	gen := e.generations[conv]
	e.mu.Unlock()
	if e.journal != nil {
		if err := e.journal.SaveGeneration(context.Background(), conv, gen); err != nil {
			e.log.Error("persist generation failed", "error", err)
		}
	}
	return stopped
}

// Messages 返回某个会话的消息快照（按创建时间排序）。
