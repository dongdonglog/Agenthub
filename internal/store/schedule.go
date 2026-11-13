package store

import (
	"context"
	"time"
)

// ScheduledTask 是一条定时任务。
type ScheduledTask struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Cron       string     `json:"cron"`
	Timezone   string     `json:"timezone"`
	ChatID     string     `json:"chat_id"`
	Channel    string     `json:"channel"`
	Prompt     string     `json:"prompt"`
	Enabled    bool       `json:"enabled"`
	NextRunAt  *time.Time `json:"next_run_at"`
	LastRunAt  *time.Time `json:"last_run_at"`
	LastStatus string     `json:"last_status"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ListTasks 返回全部定时任务。
func (j *Journal) ListTasks(ctx context.Context) ([]ScheduledTask, error) {
	rows, err := j.pool.Query(ctx, `
		SELECT id, name, cron, timezone, chat_id, channel, prompt, enabled,
		       next_run_at, last_run_at, last_status, created_at
		FROM scheduled_task ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ScheduledTask, 0)
	for rows.Next() {
		var t ScheduledTask
		if err := rows.Scan(&t.ID, &t.Name, &t.Cron, &t.Timezone, &t.ChatID, &t.Channel,
			&t.Prompt, &t.Enabled, &t.NextRunAt, &t.LastRunAt, &t.LastStatus, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTask 读取单个任务。
func (j *Journal) GetTask(ctx context.Context, id string) (ScheduledTask, error) {
	var t ScheduledTask
	err := j.pool.QueryRow(ctx, `
		SELECT id, name, cron, timezone, chat_id, channel, prompt, enabled,
		       next_run_at, last_run_at, last_status, created_at
		FROM scheduled_task WHERE id = $1`, id).Scan(&t.ID, &t.Name, &t.Cron, &t.Timezone,
		&t.ChatID, &t.Channel, &t.Prompt, &t.Enabled, &t.NextRunAt, &t.LastRunAt, &t.LastStatus, &t.CreatedAt)
	return t, err
}

// SaveTask 新建或更新任务。
func (j *Journal) SaveTask(ctx context.Context, t ScheduledTask) error {
	_, err := j.pool.Exec(ctx, `
		INSERT INTO scheduled_task (id, name, cron, timezone, chat_id, channel, prompt, enabled, next_run_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			cron = EXCLUDED.cron,
			timezone = EXCLUDED.timezone,
			chat_id = EXCLUDED.chat_id,
			channel = EXCLUDED.channel,
			prompt = EXCLUDED.prompt,
			enabled = EXCLUDED.enabled,
			next_run_at = EXCLUDED.next_run_at`,
		t.ID, t.Name, t.Cron, t.Timezone, t.ChatID, t.Channel, t.Prompt, t.Enabled, t.NextRunAt)
	return err
}

// DeleteTask 删除任务。
func (j *Journal) DeleteTask(ctx context.Context, id string) error {
	_, err := j.pool.Exec(ctx, `DELETE FROM scheduled_task WHERE id = $1`, id)
	return err
}

// ClaimDue 抢占到期任务：置空 next_run_at 并返回，避免重复执行。
func (j *Journal) ClaimDue(ctx context.Context, now time.Time) ([]ScheduledTask, error) {
	rows, err := j.pool.Query(ctx, `
		UPDATE scheduled_task SET next_run_at = NULL, last_run_at = $1
		WHERE id IN (
			SELECT id FROM scheduled_task
			WHERE enabled = true AND next_run_at IS NOT NULL AND next_run_at <= $1
			ORDER BY next_run_at
			FOR UPDATE SKIP LOCKED
			LIMIT 20
		)
		RETURNING id, name, cron, timezone, chat_id, channel, prompt, enabled`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ScheduledTask, 0)
	for rows.Next() {
		var t ScheduledTask
		if err := rows.Scan(&t.ID, &t.Name, &t.Cron, &t.Timezone, &t.ChatID, &t.Channel, &t.Prompt, &t.Enabled); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// FinishTask 记录执行结果与下次时间。
func (j *Journal) FinishTask(ctx context.Context, id string, next time.Time, status string) error {
	_, err := j.pool.Exec(ctx, `
		UPDATE scheduled_task SET next_run_at = $2, last_status = $3, last_run_at = now() WHERE id = $1`, id, next, status)
	return err
}
