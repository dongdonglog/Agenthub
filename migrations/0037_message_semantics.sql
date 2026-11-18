-- 0037_message_semantics.sql — 消息语义字段
--
-- message_state 此前只存了展示用字段。重启恢复后 NoReply / ReadOnly / ToolAllow /
-- CaseID 全部丢失，导致：
--   1. AIOps 诊断消息恢复后不再只读，工具白名单也丢了（安全回归）；
--   2. 内部合成 ID（如 incident:<id>:firing）被当成飞书 message_id 去回复，投递必然 400。

ALTER TABLE message_state ADD COLUMN IF NOT EXISTS case_id text NOT NULL DEFAULT '';
ALTER TABLE message_state ADD COLUMN IF NOT EXISTS no_reply boolean NOT NULL DEFAULT false;
ALTER TABLE message_state ADD COLUMN IF NOT EXISTS read_only boolean NOT NULL DEFAULT false;
ALTER TABLE message_state ADD COLUMN IF NOT EXISTS tool_allow jsonb NOT NULL DEFAULT '[]'::jsonb;

-- 回填：这三个前缀是内部合成 ID（告警/工单/定时任务），不是平台消息 ID，
-- 不能作为 reply_to 使用。历史上没有 no_reply 列，重启后会被误当作可回复消息。
UPDATE message_state SET no_reply = true
WHERE no_reply = false
  AND (external_id LIKE 'incident:%' OR external_id LIKE 'ticket:%' OR external_id LIKE 'cron\_%');

-- 回填：AI 触发的诊断/支持运行一律只读，历史上未持久化该字段。
UPDATE message_state SET read_only = true
WHERE read_only = false
  AND (conversation_id LIKE 'aiops:%' OR conversation_id LIKE 'support:%');

