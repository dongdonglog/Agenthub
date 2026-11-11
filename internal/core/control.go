package core

import (
	"context"
	"sort"
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

// SetIdentity 更新会话展示信息（飞书群名/用户名）。
func (e *Engine) SetIdentity(conv, chatName, userName string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.identities[conv] = Identity{ChatName: chatName, UserName: userName}
}

// SessionInfos 返回带展示名的会话列表。
func (e *Engine) SessionInfos() []SessionInfo {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]SessionInfo, 0, len(e.identities)+len(e.order))
	seen := make(map[string]bool, len(e.order))
	counts := make(map[string]int)
	latestByConv := make(map[string]time.Time)
	for _, m := range e.messages {
		counts[m.ConversationID]++
		if m.CreatedAt.After(latestByConv[m.ConversationID]) {
			latestByConv[m.ConversationID] = m.CreatedAt
		}
	}
	for conv := range counts {
		seen[conv] = true
		info := SessionInfo{ID: conv, Messages: counts[conv]}
		if ident, ok := e.identities[conv]; ok {
			info.ChatName = ident.ChatName
			info.UserName = ident.UserName
		}
		info.DisplayName = displayName(info)
		latest := latestByConv[conv]
		if !latest.IsZero() {
			info.LastActive = latest.Format("2006-01-02 15:04")
		}
		out = append(out, info)
	}
	for conv, ident := range e.identities {
		if seen[conv] {
			continue
		}
		info := SessionInfo{ID: conv, ChatName: ident.ChatName, UserName: ident.UserName}
		info.DisplayName = displayName(info)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive > out[j].LastActive })
	return out
}

func displayName(info SessionInfo) string {
	switch {
	case info.ChatName != "" && info.UserName != "":
		return info.ChatName + " · " + info.UserName
	case info.ChatName != "":
		return info.ChatName
	case info.UserName != "":
		return info.UserName
	default:
		return info.ID
	}
}
