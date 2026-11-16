-- 0022_run_round.sql — 群聊轮次编号

ALTER TABLE run_state ADD COLUMN IF NOT EXISTS chat_id text NOT NULL DEFAULT '';
ALTER TABLE run_state ADD COLUMN IF NOT EXISTS round_no integer NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS run_state_chat_round_idx ON run_state (chat_id, round_no);
