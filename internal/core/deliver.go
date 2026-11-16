package core

import (
	"context"
	"fmt"
	"time"

	"github.com/moying/agenthub/internal/channel"
)

// send 领取并发送一条投递。
//
// @remarks 领取与终态更新都校验租约令牌：同一投递只会有一个发送者进入通道调用。
// 通道明确未接受（限流）才重试；网络/超时等结果不明进入 ambiguous，不自动重发。
func (e *Engine) send(ctx context.Context, d *Delivery) {
	if e.ch == nil {
		e.mu.Lock()
		d.State = "sent"
		e.sent[d.IdempotencyKey] = true
		e.mu.Unlock()
		e.persistDelivery(ctx, d)
		return
	}

	if err := e.waitOutbound(ctx); err != nil {
		return
	}
	claimed, err := e.claimDelivery(ctx, d)
	if err != nil {
		e.log.Error("claim delivery failed", "delivery", d.IdempotencyKey, "error", err)
		return
	}
	if !claimed {
		return
	}
	if ok, err := e.markDeliveryCalling(ctx, d); err != nil {
		e.log.Error("mark delivery calling failed", "delivery", d.IdempotencyKey, "error", err)
		return
	} else if !ok {
		e.log.Warn("delivery lease lost before channel call", "delivery", d.IdempotencyKey)
		return
	}

	var receipt channel.Receipt
	if d.Kind == "" || d.Kind == "text" {
		receipt, err = e.ch.SendText(ctx, channel.Outbound{ChatID: d.ChatID, ReplyTo: d.ReplyTo, Text: d.Text})
	} else if sender, ok := e.ch.(channel.ArtifactSender); ok {
		if d.ArtifactPath == "" {
			err = fmt.Errorf("产物路径为空")
		} else {
			receipt, err = sender.SendArtifact(ctx, channel.OutboundArtifact{
				ChatID: d.ChatID, ReplyTo: d.ReplyTo,
				Artifact: channel.Artifact{Name: d.Name, Path: d.ArtifactPath, Kind: d.Kind},
			})
		}
	} else {
		err = channel.ErrUnsupported
	}

	attempt := d.Attempts
	if err == nil {
		if e.finishDelivery(ctx, d, "sent", "", receipt.ExternalID, time.Time{}) {
			e.mu.Lock()
			e.sent[d.IdempotencyKey] = true
			e.mu.Unlock()
			e.log.Info("delivery sent", "run", d.RunID, "trace", d.TraceID)
		}
		return
	}

	kind := channel.FailureKindOf(err)
	if ctx.Err() != nil {
		kind = channel.FailureAmbiguous
	}
	state := "failed"
	retry := false
	if kind == channel.FailureRetryable && attempt < maxDeliveryAttempts {
		state = "retrying"
		retry = true
	} else if kind == channel.FailureAmbiguous {
		state = "ambiguous"
	}

	var next time.Time
	var delay time.Duration
	if retry {
		delay = e.retryDelay(attempt)
		next = e.now().Add(delay)
	}
	if !e.finishDelivery(ctx, d, state, err.Error(), "", next) {
		e.log.Warn("delivery lease lost before finish", "delivery", d.IdempotencyKey)
		return
	}
	if e.metrics != nil {
		e.metrics.Inc("agenthub_deliveries_total", map[string]string{"result": state})
	}
	if !retry {
		e.log.Warn("delivery stopped", "run", d.RunID, "trace", d.TraceID, "state", state, "attempt", attempt, "error", err)
		return
	}
	e.log.Warn("delivery retry scheduled", "run", d.RunID, "trace", d.TraceID, "attempt", attempt, "delay", delay, "error", err)
	time.AfterFunc(delay, func() {
		e.send(context.Background(), d)
	})
}

// waitOutbound 在跨会话共享令牌桶中等待一个发送名额。
func (e *Engine) waitOutbound(ctx context.Context) error {
	for {
		e.mu.Lock()
		if e.outboundQPS <= 0 {
			e.mu.Unlock()
			return nil
		}
		now := time.Now()
		if e.outboundLast.IsZero() {
			e.outboundLast = now
		}
		e.outboundTokens += now.Sub(e.outboundLast).Seconds() * float64(e.outboundQPS)
		if e.outboundTokens > 1 {
			e.outboundTokens = 1
		}
		e.outboundLast = now
		if e.outboundTokens >= 1 {
			e.outboundTokens--
			e.mu.Unlock()
			return nil
		}
		wait := time.Duration((1 - e.outboundTokens) / float64(e.outboundQPS) * float64(time.Second))
		e.mu.Unlock()
		if wait <= 0 {
			wait = time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// claimDelivery 领取投递。数据库模式使用条件更新；无库模式用内存状态模拟同一语义。
func (e *Engine) claimDelivery(ctx context.Context, d *Delivery) (bool, error) {
	if e.journal != nil {
		claimed, ok, err := e.journal.ClaimDelivery(ctx, d.IdempotencyKey, e.deliveryOwner, newLeaseToken(), e.deliveryLease)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		*d = *claimed
		return true, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	if d.State != "" && d.State != "pending" && d.State != "retrying" {
		return false, nil
	}
	if !d.NextAttempt.IsZero() && d.NextAttempt.After(now) {
		return false, nil
	}
	if d.LeaseToken != "" && d.LeaseExpiresAt.After(now) {
		return false, nil
	}
	d.Attempts++
	d.State = "sending"
	d.SendPhase = "claimed"
	d.LeaseOwner = e.deliveryOwner
	d.LeaseToken = newLeaseToken()
	d.LeaseExpiresAt = now.Add(e.deliveryLease)
	d.SendStartedAt = now
	d.Error = ""
	return true, nil
}

func (e *Engine) markDeliveryCalling(ctx context.Context, d *Delivery) (bool, error) {
	if e.journal != nil {
		return e.journal.MarkDeliveryCalling(ctx, d.IdempotencyKey, d.LeaseToken)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if d.LeaseToken == "" || d.State != "sending" || d.SendPhase != "claimed" {
		return false, nil
	}
	d.SendPhase = "calling"
	return true, nil
}

func (e *Engine) finishDelivery(ctx context.Context, d *Delivery, state, errText, externalID string, next time.Time) bool {
	if e.journal != nil {
		ok, err := e.journal.FinishDelivery(ctx, d.IdempotencyKey, d.LeaseToken, state, errText, externalID, next)
		if err != nil {
			e.log.Error("finish delivery failed", "delivery", d.IdempotencyKey, "error", err)
			return false
		}
		if !ok {
			return false
		}
	}
	e.mu.Lock()
	d.State = state
	d.Error = errText
	d.ExternalID = externalID
	d.NextAttempt = next
	d.LeaseOwner = ""
	d.LeaseToken = ""
	d.LeaseExpiresAt = time.Time{}
	d.SendPhase = ""
	e.mu.Unlock()
	return true
}

func (e *Engine) persistDelivery(ctx context.Context, d *Delivery) {
	if e.journal == nil {
		return
	}
	if err := e.journal.SaveDelivery(ctx, d); err != nil {
		e.log.Error("persist delivery failed", "error", err, "delivery", d.IdempotencyKey)
	}
}

// RecoverDeliveries 扫描过期租约；claimed 重新入队，calling 标记歧义。
func (e *Engine) RecoverDeliveries(ctx context.Context) {
	e.recoverDeliveries(ctx, true)
}

func (e *Engine) recoverDeliveries(ctx context.Context, retry bool) {
	if e.journal == nil {
		return
	}
	items, err := e.journal.RecoverDeliveries(ctx, 100)
	if err != nil {
		e.log.Warn("recover deliveries failed", "error", err)
		return
	}
	for _, d := range items {
		e.mu.Lock()
		if existing, ok := e.deliveries[d.IdempotencyKey]; ok {
			*existing = *d
		} else {
			e.deliveries[d.IdempotencyKey] = d
		}
		e.mu.Unlock()
		if d.State == "ambiguous" {
			if e.metrics != nil {
				e.metrics.Inc("agenthub_deliveries_total", map[string]string{"result": "ambiguous"})
			}
			e.log.Warn("delivery recovered as ambiguous", "run", d.RunID, "trace", d.TraceID)
			continue
		}
		e.log.Info("delivery recovered for retry", "delivery", d.IdempotencyKey)
		if retry {
			go e.send(ctx, d)
		}
	}
}
