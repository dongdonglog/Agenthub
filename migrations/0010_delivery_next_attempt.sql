-- 0010_delivery_next_attempt.sql — 记录下次投递时间，重启后按原节奏补偿

ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz;
