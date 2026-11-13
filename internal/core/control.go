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

// SetGenerationCleaner 设置旧会话代清理回调（删除工作区/会话文件）。
func (e *Engine) SetGenerationCleaner(fn func(conv string, generation int)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.genCleaner = fn
}

func (e *Engine) Reset(conv string) bool {
	stopped := e.Stop(conv)
	e.Clear(conv)
	e.mu.Lock()
	oldGen := e.generationLocked(conv)
	e.generations[conv] = oldGen + 1
	gen := e.generations[conv]
	e.mu.Unlock()
	if e.journal != nil {
		if err := e.journal.SaveGeneration(context.Background(), conv, gen); err != nil {
			e.log.Error("persist generation failed", "error", err)
		}
	}
	if e.genCleaner != nil {
		go e.genCleaner(conv, oldGen)
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

	counts := make(map[string]int)
	latestByConv := make(map[string]time.Time)
	kindByConv := make(map[string]string)
	for _, m := range e.messages {
		counts[m.ConversationID]++
		if m.CreatedAt.After(latestByConv[m.ConversationID]) {
			latestByConv[m.ConversationID] = m.CreatedAt
		}
		if kindByConv[m.ConversationID] == "" && m.ChatKind != "" {
			kindByConv[m.ConversationID] = m.ChatKind
		}
	}

	out := make([]SessionInfo, 0, len(counts)+len(e.identities))
	seen := make(map[string]bool, len(counts))
	for conv := range counts {
		seen[conv] = true
		out = append(out, e.sessionInfoLocked(conv, counts[conv], kindByConv[conv], latestByConv[conv]))
	}
	for conv, ident := range e.identities {
		if seen[conv] {
			continue
		}
		info := e.sessionInfoLocked(conv, 0, "", time.Time{})
		info.ChatName = ident.ChatName
		info.UserName = ident.UserName
		info.DisplayName = displayName(info)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive > out[j].LastActive })
	return out
}

func (e *Engine) sessionInfoLocked(conv string, messages int, chatKind string, latest time.Time) SessionInfo {
	info := SessionInfo{ID: conv, Messages: messages}
	if ident, ok := e.identities[conv]; ok {
		info.ChatName = ident.ChatName
		info.UserName = ident.UserName
	}
	parts := strings.SplitN(conv, ":", 3)
	if len(parts) == 3 {
		info.ChatID = parts[1]
		info.UserID = parts[2]
	}
	if strings.HasPrefix(conv, "cron:") {
		info.Kind = "scheduled"
		info.Internal = true
	} else if chatKind == "group" {
		info.Kind = "group"
	} else if chatKind == "p2p" {
		info.Kind = "p2p"
	} else if info.ChatName != "" {
		info.Kind = "group"
	} else if info.UserName != "" {
		info.Kind = "p2p"
	} else {
		info.Kind = "unknown"
	}
	if info.ChatName == "" && info.UserName == "" {
		info.Internal = true
	}
	if !latest.IsZero() {
		info.LastActive = latest.Format("2006-01-02 15:04")
	}
	info.DisplayName = displayName(info)
	return info
}

func displayName(info SessionInfo) string {
	switch info.Kind {
	case "scheduled":
		if info.UserName != "" {
			return "定时任务 · " + info.UserName
		}
		return "定时任务"
	case "group":
		name := "群聊"
		if info.ChatName != "" {
			name += " · " + info.ChatName
		}
		if info.UserName != "" {
			name += " · " + info.UserName
		}
		return name
	case "p2p":
		if info.UserName != "" {
			return "私聊 · " + info.UserName
		}
		return "私聊会话"
	default:
		if info.ChatName != "" && info.UserName != "" {
			return info.ChatName + " · " + info.UserName
		}
		if info.ChatName != "" {
			return info.ChatName
		}
		if info.UserName != "" {
			return info.UserName
		}
		id := info.ID
		if len(id) > 16 {
			id = id[:16] + "…"
		}
		return "会话 " + id
	}
}
