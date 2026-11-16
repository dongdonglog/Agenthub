package core

import (
	"context"
	"time"
)

// projectReaction 异步投影消息状态表情；失败只记日志，不影响运行状态。
func (e *Engine) projectReaction(msg *Message, state string) {
	if msg == nil || msg.ExternalID == "" {
		return
	}
	e.mu.Lock()
	reactor := e.reactor
	oldID := e.reactions[msg.ExternalID]
	e.mu.Unlock()
	if reactor == nil {
		return
	}
	go func() {
		e.reactionMu.Lock()
		defer e.reactionMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if oldID != "" {
			if err := reactor.Unreact(ctx, msg.ExternalID, oldID); err != nil {
				e.log.Debug("remove reaction failed", "error", err)
			}
		}
		reactionID, err := reactor.React(ctx, msg.ExternalID, state)
		if err != nil {
			e.log.Debug("project reaction failed", "state", state, "error", err)
			return
		}
		e.mu.Lock()
		if reactionID == "" {
			delete(e.reactions, msg.ExternalID)
		} else {
			e.reactions[msg.ExternalID] = reactionID
		}
		journal := e.journal
		e.mu.Unlock()
		if journal != nil && reactionID != "" {
			if err := journal.SaveReaction(ctx, msg.ExternalID, state, reactionID); err != nil {
				e.log.Debug("persist reaction failed", "error", err)
			}
		}
	}()
}
