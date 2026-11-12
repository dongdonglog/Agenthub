-- 0016_scheduled_task.sql — 定时任务

CREATE TABLE IF NOT EXISTS scheduled_task (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    cron        text NOT NULL,
    timezone    text NOT NULL DEFAULT 'Asia/Shanghai',
    chat_id     text NOT NULL,
    channel     text NOT NULL DEFAULT 'feishu',
    prompt      text NOT NULL,
    enabled     boolean NOT NULL DEFAULT true,
    next_run_at timestamptz,
    last_run_at timestamptz,
    last_status text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS scheduled_task_due_idx ON scheduled_task (enabled, next_run_at);
