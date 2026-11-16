package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/moying/agenthub/internal/channel"
)

func (e *Engine) send(ctx context.Context, d *Delivery) {
	if e.ch == nil {
		e.mu.Lock()
		d.State = "sent"
		e.sent[d.IdempotencyKey] = true
		e.mu.Unlock()
		e.persistDelivery(ctx, d)
		return
	}
	e.mu.Lock()
	d.Attempts++
	d.State = "sending"
	d.SendStartedAt = e.now()
	attempt := d.Attempts
	e.mu.Unlock()
	// 先落“发送中”再调用通道：进程在请求中途退出时，重启后只能标记歧义，
	// 不能安全地自动重发。
	e.persistDelivery(ctx, d)

	var err error
	if d.Kind == "" || d.Kind == "text" {
		_, err = e.ch.SendText(ctx, channel.Outbound{ChatID: d.ChatID, ReplyTo: d.ReplyTo, Text: d.Text})
	} else if sender, ok := e.ch.(channel.ArtifactSender); ok {
		if d.ArtifactPath == "" {
			err = fmt.Errorf("产物路径为空")
		} else {
			_, err = sender.SendArtifact(ctx, channel.OutboundArtifact{
				ChatID: d.ChatID, ReplyTo: d.ReplyTo,
				Artifact: channel.Artifact{Name: d.Name, Path: d.ArtifactPath, Kind: d.Kind},
			})
		}
	} else {
		err = channel.ErrUnsupported
	}
	e.mu.Lock()
	if err == nil {
		d.State = "sent"
		d.Error = ""
		d.NextAttempt = time.Time{}
		e.sent[d.IdempotencyKey] = true
		e.mu.Unlock()
		e.persistDelivery(ctx, d)
		e.log.Info("delivery sent", "run", d.RunID, "trace", d.TraceID)
		return
	}
	d.Error = err.Error()
	retry := attempt < maxDeliveryAttempts && !errors.Is(err, channel.ErrUnsupported) && ctx.Err() == nil
	if retry {
		d.State = "retrying"
	} else {
		d.State = "failed"
	}
	e.mu.Unlock()
	if e.metrics != nil {
		e.metrics.Inc("agenthub_deliveries_total", map[string]string{"result": d.State})
	}

	if !retry {
		e.log.Warn("delivery failed permanently", "run", d.RunID, "trace", d.TraceID, "attempts", attempt, "error", err)
		return
	}
	delay := e.retryDelay(attempt)
	e.mu.Lock()
	d.NextAttempt = time.Now().Add(delay)
	e.mu.Unlock()
	e.persistDelivery(ctx, d)
	e.log.Warn("delivery failed; scheduling retry", "run", d.RunID, "trace", d.TraceID, "attempt", attempt, "delay", delay, "error", err)
	time.AfterFunc(delay, func() {
		e.send(context.Background(), d)
	})
}

func (e *Engine) persistDelivery(ctx context.Context, d *Delivery) {
	if e.journal == nil {
		return
	}
	if err := e.journal.SaveDelivery(ctx, d); err != nil {
		e.log.Error("persist delivery failed", "error", err, "delivery", d.IdempotencyKey)
	}
}
