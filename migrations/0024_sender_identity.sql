-- 0024_sender_identity.sql — 批次内发送者身份

ALTER TABLE message_state ADD COLUMN IF NOT EXISTS sender_name text NOT NULL DEFAULT '';
