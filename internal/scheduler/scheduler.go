// Package scheduler 负责定时任务的调度与执行。
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/moying/agenthub/internal/channel"
	"github.com/moying/agenthub/internal/core"
	"github.com/moying/agenthub/internal/store"
)

// Scheduler 周期检查到期任务并触发运行。
type Scheduler struct {
	journal *store.Journal
	engine  *core.Engine
	log     *slog.Logger
}

// New 构造调度器。
func New(journal *store.Journal, engine *core.Engine, log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{journal: journal, engine: engine, log: log}
}

// NextRun 计算下一次执行时间。
//
// @remarks 使用标准 5 段 cron（分 时 日 月 周），按任务时区解释。
func NextRun(expr, tz string, from time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("时区非法：%s", tz)
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("cron 表达式非法：%w", err)
	}
	return sched.Next(from.In(loc)), nil
}

// Run 启动调度循环，直到 ctx 结束。
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	tasks, err := s.journal.ClaimDue(ctx, time.Now())
	if err != nil {
		s.log.Error("scheduler claim failed", "error", err)
		return
	}
	for _, task := range tasks {
		s.execute(ctx, task)
	}
}

// execute 触发一次任务并记录结果与下次时间。
func (s *Scheduler) execute(ctx context.Context, task store.ScheduledTask) {
	status := "ok"
	if err := s.submit(ctx, task); err != nil {
		status = "failed: " + err.Error()
		s.log.Error("scheduled task failed", "task", task.ID, "error", err)
	}
	next, err := NextRun(task.Cron, task.Timezone, time.Now())
	if err != nil {
		next = time.Now().Add(24 * time.Hour)
		s.log.Error("scheduled task next run failed", "task", task.ID, "error", err)
	}
	if err := s.journal.FinishTask(ctx, task.ID, next, status); err != nil {
		s.log.Error("scheduled task finish failed", "task", task.ID, "error", err)
	}
}

// RunNow 立即执行一次任务，记录结果但不改变既定的下次时间。
func (s *Scheduler) RunNow(ctx context.Context, task store.ScheduledTask) error {
	status := "ok"
	if err := s.submit(ctx, task); err != nil {
		status = "failed: " + err.Error()
	}
	next := time.Now()
	if task.NextRunAt != nil {
		next = *task.NextRunAt
	} else if computed, err := NextRun(task.Cron, task.Timezone, time.Now()); err == nil {
		next = computed
	}
	if err := s.journal.FinishTask(ctx, task.ID, next, status); err != nil {
		s.log.Warn("task run-now status failed", "task", task.ID, "error", err)
	}
	return nil
}

func (s *Scheduler) submit(ctx context.Context, task store.ScheduledTask) error {
	ext := fmt.Sprintf("cron_%s_%d", task.ID, time.Now().UnixNano())
	s.engine.SetIdentity("cron:"+task.ChatID+":scheduler", "定时任务", task.Name)
	_, err := s.engine.Submit(ctx, channel.Inbound{
		Channel:    "cron",
		Tenant:     "cron",
		ExternalID: ext,
		SentAt:     time.Now(),
		Sender:     channel.Sender{OpenID: "scheduler", Name: "定时任务"},
		Chat:       channel.Chat{ID: task.ChatID, Kind: "group"},
		Text:       task.Prompt,
		MsgType:    "text",
		NoReply:    true,
	})
	return err
}
