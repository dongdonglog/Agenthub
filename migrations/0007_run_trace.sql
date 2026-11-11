-- 0007_run_trace.sql — 运行事件（阶段与工具调用）持久化
-- 注：0001 中的 run_event 使用 uuid 关联 run(id)，与轻量 run_state 不兼容，另建表。

CREATE TABLE IF NOT EXISTS run_trace (
    id          bigserial PRIMARY KEY,
    run_id      text NOT NULL,
    kind        text NOT NULL,
    name        text NOT NULL DEFAULT '',
    detail      text NOT NULL DEFAULT '',
    duration_ms bigint NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS run_trace_run_idx ON run_trace (run_id, id);
