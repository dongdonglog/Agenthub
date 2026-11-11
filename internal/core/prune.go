package core

import (
	"sort"
	"time"
)

func (e *Engine) Prune() {
	e.mu.Lock()
	e.pruneAt = time.Time{}
	e.mu.Unlock()
	e.prune()
}

func (e *Engine) prune() {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if now.Sub(e.pruneAt) < 30*time.Second {
		return
	}
	e.pruneAt = now

	// 1) 每个会话保留最近 N 条非活动消息
	byConv := make(map[string][]*Message)
	for _, m := range e.messages {
		byConv[m.ConversationID] = append(byConv[m.ConversationID], m)
	}
	for conv, list := range byConv {
		if len(list) <= e.maxMessagesPerConv {
			continue
		}
		sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
		drop := len(list) - e.maxMessagesPerConv
		for _, m := range list {
			if drop == 0 {
				break
			}
			if m.Status == "queued" || m.Status == "processing" {
				continue
			}
			delete(e.messages, m.ID)
			delete(e.dedupe, conv+"|"+m.ExternalID)
			drop--
		}
	}

	// 2) 会话数量上限：淘汰最久未活跃且空闲的会话
	if len(byConv) > e.maxConversations {
		type convState struct {
			id     string
			active time.Time
			idle   bool
		}
		states := make([]convState, 0, len(byConv))
		for conv, list := range byConv {
			latest := time.Time{}
			idle := e.active[conv] == ""
			for _, m := range list {
				if m.CreatedAt.After(latest) {
					latest = m.CreatedAt
				}
				if m.Status == "queued" || m.Status == "processing" {
					idle = false
				}
			}
			states = append(states, convState{id: conv, active: latest, idle: idle})
		}
		sort.Slice(states, func(i, j int) bool { return states[i].active.Before(states[j].active) })
		remove := len(states) - e.maxConversations
		for _, st := range states {
			if remove == 0 {
				break
			}
			if !st.idle {
				continue
			}
			for id, m := range e.messages {
				if m.ConversationID == st.id {
					delete(e.messages, id)
					delete(e.dedupe, st.id+"|"+m.ExternalID)
				}
			}
			remove--
		}
	}

	// 3) 运行与投递上限（保留最近）
	if len(e.runOrder) > e.maxRuns {
		excess := len(e.runOrder) - e.maxRuns
		kept := e.runOrder[:0]
		for _, id := range e.runOrder {
			if excess > 0 {
				if _, active := e.active[runConv(e.runs[id])]; !active {
					delete(e.runs, id)
					delete(e.events, id)
					excess--
					continue
				}
			}
			kept = append(kept, id)
		}
		e.runOrder = kept
	}
	if len(e.deliveries) > e.maxDeliveries {
		list := make([]*Delivery, 0, len(e.deliveries))
		for _, d := range e.deliveries {
			list = append(list, d)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
		for _, d := range list[:len(list)-e.maxDeliveries] {
			if d.State == "pending" || d.State == "retrying" {
				continue
			}
			delete(e.deliveries, d.IdempotencyKey)
		}
	}
}

func runConv(r *Run) string {
	if r == nil {
		return ""
	}
	return r.ConversationID
}

// Flush 处理所有"有排队消息且无活动运行"的会话。
//
// @remarks 生产环境由定时器驱动；测试可以显式调用以获得确定性。
