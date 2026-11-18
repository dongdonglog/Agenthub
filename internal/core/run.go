package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moying/agenthub/internal/agent"
)

// clearPending 释放本进程的「待执行」占位；只在占位仍属于 pending 时清除。
//
// @remarks 领取阶段可能已被别的路径推进为真实 runID，此时不能覆盖。
func (e *Engine) clearPending(conv string) {
	e.mu.Lock()
	if e.active[conv] == "pending" {
		delete(e.active, conv)
	}
	e.mu.Unlock()
}

// requeue 把未能领取的消息放回本地队列，等下一轮调度重试。
//
// @remarks 数据库不可用时领不到，但消息本身仍然有效；直接丢弃会让它们停在
// queued 直到进程重启。
func (e *Engine) requeue(conv string, ids []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, id := range ids {
		if m := e.messages[id]; m != nil && m.Status == "queued" {
			e.order[conv] = append(e.order[conv], id)
		}
	}
}

// syncMessageStatuses 用数据库状态校准本地副本。
//
// @remarks 消息可能已被其它实例领走并执行完；本地若仍记为 queued，
// 下一轮会拿它再试一次。校准后本地记忆与事实源保持一致。
func (e *Engine) syncMessageStatuses(ctx context.Context, ids []string) {
	if e.journal == nil || len(ids) == 0 {
		return
	}
	statuses, err := e.journal.LoadMessageStatuses(ctx, ids)
	if err != nil {
		e.log.Debug("load message statuses failed", "error", err)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, status := range statuses {
		if m := e.messages[id]; m != nil {
			m.Status = status
		}
	}
}

func (e *Engine) runBatch(ctx context.Context, conv string) error {
	// 跨实例互斥：先拿会话执行租约。拿不到说明别的实例正在跑这个会话，
	// 本实例直接跳过——消息由持有者处理，不能两边同时执行。
	if e.journal != nil {
		held, err := e.journal.TryAcquireConversationLease(ctx, conv, e.deliveryOwner, e.leaseTTL)
		if err != nil {
			e.log.Warn("acquire conversation lease failed", "conversation", conv, "error", err)
			// 不能留下 pending 占位，否则该会话再也不会被调度。
			e.clearPending(conv)
			return err
		}
		if !held {
			// 其它实例持有执行权；本地队列保持不动，由持有者处理。
			e.clearPending(conv)
			return nil
		}
		defer func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := e.journal.ReleaseConversationLease(releaseCtx, conv, e.deliveryOwner); err != nil {
				e.log.Debug("release conversation lease failed", "conversation", conv, "error", err)
			}
		}()
	}
	e.mu.Lock()
	// 「从空闲到占用」必须是原子转换：任何非空值（包括 pending）都表示已有执行者。
	// 若在这里放行 pending，Dispatch 与 Flush 的并发调用会双双进入同一会话。
	if e.active[conv] != "" {
		e.mu.Unlock()
		return nil
	}
	// 占用本进程的执行槽位，避免同一进程内并发进入同一会话。
	e.active[conv] = "pending"
	candidates := make([]string, 0, len(e.order[conv]))
	for _, id := range e.order[conv] {
		if m := e.messages[id]; m != nil && m.Status == "queued" {
			candidates = append(candidates, id)
		}
	}
	// 批次快照：清空队列并冻结输入（不变量 I2）。新消息进下一批。
	e.order[conv] = nil
	e.mu.Unlock()

	if len(candidates) == 0 {
		e.clearPending(conv)
		return nil
	}

	// 原子领取：只有数据库里仍是 queued 的消息才归本次运行。
	//
	// @remarks 跨实例场景下本地队列可能持有已被别的实例处理过的消息（例如另一实例
	// 先恢复又先执行完）。这里以数据库为唯一事实源，领不到的不执行。
	claimed := candidates
	if e.journal != nil {
		got, err := e.journal.ClaimMessages(ctx, candidates)
		if err != nil {
			e.log.Error("claim messages failed", "conversation", conv, "error", err)
			// 领取失败不代表消息不该执行：放回本地队列，等下一轮重试，
			// 否则它们会停在 queued 直到进程重启。
			e.requeue(conv, candidates)
			e.clearPending(conv)
			return err
		}
		claimed = got
	}
	if len(claimed) == 0 {
		// 全部已被别的实例领走：同步本地副本状态，避免本地记忆与事实源分叉。
		e.syncMessageStatuses(ctx, candidates)
		e.clearPending(conv)
		return nil
	}

	ttl := e.queueTTL
	var batch, expired []*Message
	now := e.now()
	e.mu.Lock()
	for _, id := range claimed {
		m := e.messages[id]
		if m == nil {
			continue
		}
		if ttl > 0 && m.CreatedAt.Before(now.Add(-ttl)) {
			m.Status = "expired"
			expired = append(expired, m)
			continue
		}
		m.Status = "processing"
		batch = append(batch, m)
	}
	if len(batch) == 0 {
		if e.active[conv] == "pending" {
			delete(e.active, conv)
		}
		e.mu.Unlock()
		// 这批消息已由本实例领取，标记 expired 不会与其它实例冲突。
		e.persistExpired(ctx, expired)
		e.notifyExpired(ctx, expired, ttl)
		return nil
	}
	queueNotice := ""
	if len(expired) > 0 {
		queueNotice = fmt.Sprintf("有 %d 条消息在队列中等待超过 %s，未执行。请重新发送。", len(expired), formatQueueTTL(ttl))
	}
	e.runSeq++
	e.rounds[batch[0].ChatID]++
	run := &Run{
		TraceID:        batch[0].TraceID,
		ID:             newRunID(e.runSeq),
		ConversationID: conv,
		ChatID:         batch[0].ChatID,
		Round:          e.rounds[batch[0].ChatID],
		State:          "running",
		StartedAt:      e.now(),
	}
	for _, m := range batch {
		run.InputIDs = append(run.InputIDs, m.ID)
	}
	e.runs[run.ID] = run
	e.runOrder = append(e.runOrder, run.ID)
	e.active[conv] = run.ID

	runCtx, cancel := context.WithCancel(ctx)
	e.cancels[conv] = cancel
	e.mu.Unlock()

	e.projectReaction(batch[0], "processing")
	e.persistExpired(ctx, expired)
	if e.journal != nil {
		if err := e.journal.SaveRun(ctx, run); err != nil {
			e.log.Error("persist run failed", "error", err, "run", run.ID)
		}
		// 输入消息状态已由 ClaimMessages 置为 processing，无需重复写入。
	}

	var cardHandle string
	if e.presenter != nil {
		handle, err := e.presenter.Open(ctx, Presentation{
			ConversationID: conv,
			ChatID:         batch[0].ChatID,
			ReplyTo:        replyTarget(batch[0]),
			RunID:          run.ID,
			Inputs:         len(batch),
		})
		if err != nil {
			e.log.Warn("presenter open failed; fallback to text", "error", err, "run", run.ID)
		} else {
			cardHandle = handle
			// 立刻持久化卡片句柄：进程若在此后崩溃，回收时才能把这张卡片改成中断态，
			// 否则它会永远停在「处理中」。
			e.mu.Lock()
			run.CardHandle = handle
			e.mu.Unlock()
			if e.journal != nil {
				if err := e.journal.SaveRun(ctx, run); err != nil {
					e.log.Warn("persist card handle failed", "run", run.ID, "error", err)
				}
			}
		}
	}
	stopHeartbeat := e.startRunHeartbeat(runCtx, run.ID, conv)
	defer stopHeartbeat()

	lastProgress := time.Time{}
	updateCard := func(phase string) {
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
	progress := func(phase string) {
		e.recordEvent(run.ID, RunEvent{Kind: "phase", Name: phase})
		updateCard(phase)
	}

	binding := e.profileFor(ctx, conv, batch[0].Generation)
	e.log.Info("run started", "run", run.ID, "trace", run.TraceID, "inputs", len(batch), "profile", binding.Name)
	input := agent.Input{
		TraceID:        run.TraceID,
		RunID:          run.ID,
		ReadOnly:       batch[0].ReadOnly,
		ToolAllow:      batch[0].ToolAllow,
		CaseID:         batch[0].CaseID,
		ConversationID: conv,
		Generation:     batch[0].Generation,
		Progress:       progress,
		History:        e.history(conv, batch),
		SkillAllow:     binding.Skills,
		MCPAllow:       binding.MCPs,
		OnTool: func(name string, dur time.Duration, err error) {
			detail := "ok"
			if err != nil {
				detail = err.Error()
			}
			e.recordEvent(run.ID, RunEvent{Kind: "tool", Name: name, Detail: detail, DurationMS: dur.Milliseconds()})
			phase := "工具 · " + name
			if err != nil {
				phase = "工具失败 · " + name
			}
			updateCard(phase)
		},
	}
	for _, m := range batch {
		input.Messages = append(input.Messages, agent.Message{
			ID: m.ID, Text: m.Text, Attachments: m.Attachments,
			Sender: m.SenderOpenID, SenderName: m.SenderName, SentByAdmin: m.SentByAdmin,
		})
	}

	out, err := e.agent.Run(runCtx, input)
	cancel()

	var artifacts []*Artifact
	var artifactNotice string
	if err == nil && len(out.Artifacts) > 0 {
		artifacts, artifactNotice = e.stageArtifacts(run, out.Artifacts)
		if len(artifacts) > 0 && !e.canSendArtifacts() {
			artifactNotice = joinNotice(artifactNotice, "当前通道不支持文件交付，产物已保存，可在管理台查看。")
		}
	}

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
	if text == "" && len(artifacts) > 0 {
		text = "已生成交付物。"
	}
	if queueNotice != "" {
		if text != "" {
			text += "\n\n"
		}
		text += queueNotice
	}
	if artifactNotice != "" {
		if text != "" {
			text += "\n\n"
		}
		text += artifactNotice
	}
	if run.State == "completed" {
		run.Output = text
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
			// 记录卡片已收尾；失败时保持 false，由巡检重试，卡片不会停在「处理中」。
			run.CardClosed = true
		}
	}

	delivery := &Delivery{
		TraceID:        run.TraceID,
		CreatedAt:      e.now(),
		RunID:          run.ID,
		IdempotencyKey: run.ID + ":result",
		ChatID:         batch[0].ChatID,
		ReplyTo:        replyTarget(batch[0]),
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

	if run.State == "completed" && len(out.Report) > 0 && batch[0].CaseID != "" {
		if err := e.saveReport(ctx, batch[0].CaseID, out.Report); err != nil {
			e.log.Warn("save pi report failed", "case", batch[0].CaseID, "error", err)
		}
	}
	// 动作申请只登记待审批，不执行；执行发生在上限明确的审批路径里。
	if run.State == "completed" && len(out.Approvals) > 0 && batch[0].CaseID != "" {
		for _, raw := range out.Approvals {
			if err := e.saveApproval(ctx, batch[0].CaseID, raw); err != nil {
				e.log.Warn("save pi approval request failed", "case", batch[0].CaseID, "error", err)
			}
		}
	}

	e.projectReaction(batch[0], run.State)
	var artifactDeliveries []*Delivery
	if e.canSendArtifacts() {
		artifactDeliveries = e.buildArtifactDeliveries(run, batch[0], artifacts)
		e.mu.Lock()
		for _, item := range artifactDeliveries {
			e.deliveries[item.IdempotencyKey] = item
		}
		e.mu.Unlock()
	}

	e.appendAssistant(conv, run.ID, text)
	if e.metrics != nil {
		e.metrics.Inc("agenthub_runs_total", map[string]string{"state": run.State})
	}

	if e.journal != nil {
		for _, item := range artifactDeliveries {
			if err := e.journal.SaveDelivery(ctx, item); err != nil {
				e.log.Error("persist artifact delivery failed", "error", err, "delivery", item.IdempotencyKey)
			}
		}
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

	if !alreadySent && !cardDone {
		e.send(ctx, delivery)
	}
	for _, item := range artifactDeliveries {
		e.send(ctx, item)
	}
	return nil
}

func (e *Engine) persistExpired(ctx context.Context, expired []*Message) {
	if len(expired) == 0 || e.journal == nil {
		return
	}
	ids := make([]string, 0, len(expired))
	for _, m := range expired {
		ids = append(ids, m.ID)
	}
	if err := e.journal.SetMessageStatus(ctx, ids, "expired"); err != nil {
		e.log.Warn("persist expired messages failed", "error", err, "count", len(ids))
	}
}

func (e *Engine) notifyExpired(ctx context.Context, expired []*Message, ttl time.Duration) {
	if len(expired) == 0 {
		return
	}
	m := expired[0]
	delivery := &Delivery{
		TraceID:        m.TraceID,
		CreatedAt:      e.now(),
		IdempotencyKey: "expired:" + m.ID,
		ChatID:         m.ChatID,
		ReplyTo:        replyTarget(m),
		Text:           fmt.Sprintf("有 %d 条消息在队列中等待超过 %s，未执行。请重新发送。", len(expired), formatQueueTTL(ttl)),
		State:          "pending",
	}
	e.mu.Lock()
	e.deliveries[delivery.IdempotencyKey] = delivery
	e.mu.Unlock()
	if e.journal != nil {
		if err := e.journal.SaveDelivery(ctx, delivery); err != nil {
			e.log.Warn("persist expired notice failed", "error", err)
		}
	}
	e.send(ctx, delivery)
}

func formatQueueTTL(ttl time.Duration) string {
	if ttl >= time.Hour {
		return fmt.Sprintf("%.0f 小时", ttl.Hours())
	}
	if ttl >= time.Minute {
		return fmt.Sprintf("%.0f 分钟", ttl.Minutes())
	}
	return fmt.Sprintf("%.0f 秒", ttl.Seconds())
}

func joinNotice(existing, extra string) string {
	if existing == "" {
		return extra
	}
	if extra == "" {
		return existing
	}
	return existing + "\n" + extra
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

// replyTarget 返回投递时的回复目标；定时任务没有平台消息 ID，返回空。
func replyTarget(m *Message) string {
	if m == nil || m.NoReply {
		return ""
	}
	return m.ExternalID
}

func (e *Engine) saveReport(ctx context.Context, caseID string, raw []byte) error {
	if e.reportSink == nil {
		return nil
	}
	return e.reportSink(ctx, caseID, raw)
}

func (e *Engine) saveApproval(ctx context.Context, caseID string, raw json.RawMessage) error {
	if e.approvalSink == nil {
		return nil
	}
	return e.approvalSink(ctx, caseID, raw)
}
