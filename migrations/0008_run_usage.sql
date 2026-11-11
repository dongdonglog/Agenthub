-- 0008_run_usage.sql — 运行 token 用量

ALTER TABLE run_state ADD COLUMN IF NOT EXISTS input_tokens integer NOT NULL DEFAULT 0;
ALTER TABLE run_state ADD COLUMN IF NOT EXISTS output_tokens integer NOT NULL DEFAULT 0;
