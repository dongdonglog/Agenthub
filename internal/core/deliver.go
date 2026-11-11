package core

import (
	"context"
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
	_, err := e.ch.SendText(ctx, channel.Outbound{ChatID: d.ChatID, ReplyTo: d.ReplyTo, Text: d.Text})
	e.mu.Lock()
	d.Attempts++
	if err == nil {
		d.State = "sent"
		d.Error = ""
		e.sent[d.IdempotencyKey] = true
		e.mu.Unlock()
		e.persistDelivery(ctx, d)
		e.log.Info("delivery sent", "run", d.RunID, "trace", d.TraceID)
		return
	}
	d.Error = err.Error()
	retry := d.Attempts < maxDeliveryAttempts
	if retry {
		d.State = "retrying"
	} else {
		d.State = "failed"
	}
	attempt := d.Attempts
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
