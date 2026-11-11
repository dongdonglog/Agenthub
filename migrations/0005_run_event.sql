-- 0005_run_event.sql — 运行事件持久化（阶段与工具调用）

CREATE TABLE IF NOT EXISTS run_event (
    id          bigserial PRIMARY KEY,
    run_id      text NOT NULL,
    kind        text NOT NULL,
    name        text NOT NULL DEFAULT '',
    detail      text NOT NULL DEFAULT '',
    duration_ms bigint NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS run_event_run_idx ON run_event (run_id, id);
