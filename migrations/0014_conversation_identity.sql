-- 0014_conversation_identity.sql — 会话展示名（飞书群名/用户名）

ALTER TABLE conversation_state ADD COLUMN IF NOT EXISTS chat_name text NOT NULL DEFAULT '';
ALTER TABLE conversation_state ADD COLUMN IF NOT EXISTS user_name text NOT NULL DEFAULT '';
