-- 0020_delivery_ambiguity.sql — 发送中标记；重启后无法确认结果的投递进入歧义态

ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS send_started_at timestamptz;

CREATE INDEX IF NOT EXISTS delivery_state_sending_idx
    ON delivery_state (send_started_at) WHERE state = 'sending';
