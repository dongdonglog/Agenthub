-- 0015_conversation_priority.sql — 会话调度优先级（NULL 表示未设置）

ALTER TABLE conversation_state ADD COLUMN IF NOT EXISTS priority integer;
