-- 0004_generation.sql — 会话代：/reset 后不再携带旧上下文

ALTER TABLE message_state ADD COLUMN IF NOT EXISTS generation integer NOT NULL DEFAULT 1;
ALTER TABLE conversation_state ADD COLUMN IF NOT EXISTS generation integer NOT NULL DEFAULT 1;

ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0;
