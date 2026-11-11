-- 0002_state.sql — 持久化会话、消息、运行与投递

CREATE TABLE IF NOT EXISTS conversation_state (
    id          text PRIMARY KEY,
    chat_id     text NOT NULL,
    sender      text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    last_active timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS message_state (
    id              text PRIMARY KEY,
    conversation_id text NOT NULL,
    chat_id         text NOT NULL,
    external_id     text NOT NULL,
    sender          text NOT NULL DEFAULT '',
    text            text NOT NULL DEFAULT '',
    msg_type        text NOT NULL DEFAULT 'text',
    status          text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (conversation_id, external_id)
);

CREATE INDEX IF NOT EXISTS message_state_recent_idx ON message_state (created_at DESC);
CREATE INDEX IF NOT EXISTS message_state_queued_idx ON message_state (status) WHERE status = 'queued';

CREATE TABLE IF NOT EXISTS run_state (
    id              text PRIMARY KEY,
    conversation_id text NOT NULL,
    state           text NOT NULL,
    input_ids       jsonb NOT NULL DEFAULT '[]'::jsonb,
    output          text NOT NULL DEFAULT '',
    error           text NOT NULL DEFAULT '',
    started_at      timestamptz,
    ended_at        timestamptz
);

CREATE INDEX IF NOT EXISTS run_state_recent_idx ON run_state (started_at DESC);

CREATE TABLE IF NOT EXISTS delivery_state (
    idempotency_key text PRIMARY KEY,
    run_id          text NOT NULL,
    chat_id         text NOT NULL,
    reply_to        text NOT NULL DEFAULT '',
    text            text NOT NULL DEFAULT '',
    state           text NOT NULL,
    error           text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);
