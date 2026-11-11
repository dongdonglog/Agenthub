-- 0003_message_role.sql — 区分用户消息与助手回复，用于多轮上下文

ALTER TABLE message_state ADD COLUMN IF NOT EXISTS role text NOT NULL DEFAULT 'user';
