-- 0006_delivery_attempts.sql — 投递重试次数（0004 发布后新增的列）

ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0;
