-- 0019_artifacts.sql — 运行产物与文件投递

CREATE TABLE IF NOT EXISTS run_artifact (
    id              text PRIMARY KEY,
    run_id          text NOT NULL,
    conversation_id text NOT NULL DEFAULT '',
    name            text NOT NULL,
    kind            text NOT NULL DEFAULT 'file',
    size            bigint NOT NULL DEFAULT 0,
    sha256          text NOT NULL DEFAULT '',
    path            text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS run_artifact_run_idx ON run_artifact (run_id);

ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'text';
ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS name text NOT NULL DEFAULT '';
ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS artifact_id text NOT NULL DEFAULT '';
ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS artifact_path text NOT NULL DEFAULT '';
