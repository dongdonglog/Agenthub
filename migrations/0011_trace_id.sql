-- 0011_trace_id.sql — 请求级 trace，贯通消息、运行与投递

ALTER TABLE message_state ADD COLUMN IF NOT EXISTS trace_id text NOT NULL DEFAULT '';
ALTER TABLE run_state ADD COLUMN IF NOT EXISTS trace_id text NOT NULL DEFAULT '';
ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS trace_id text NOT NULL DEFAULT '';
