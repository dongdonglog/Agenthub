-- 0001_init.sql — AgentHub 初始表结构
-- 约定：
--   * 所有业务表带 tenant_id（v1 单租户，但结构预留多租户）
--   * run 是唯一的执行状态源；run_event 追加写、不可变
--   * 投影表（reaction_projection）只存期望态与已应用态，不作为事实源
--   * 时间统一 timestamptz

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- 租户与成员 ---------------------------------------------------------------

CREATE TABLE tenant (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_key  text NOT NULL UNIQUE,
    name        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE member (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    open_id      text NOT NULL,
    union_id     text NOT NULL DEFAULT '',
    name         text NOT NULL DEFAULT '',
    is_admin     boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, open_id)
);

-- 会话 ---------------------------------------------------------------------

CREATE TABLE chat (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    chat_id     text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('p2p', 'group')),
    title       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, chat_id)
);

-- 会话键 = (租户, 群/单聊, 成员, 话题)；thread_id 为空表示主会话
CREATE TABLE conversation (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    chat_id        uuid NOT NULL REFERENCES chat (id) ON DELETE CASCADE,
    member_id      uuid NOT NULL REFERENCES member (id) ON DELETE CASCADE,
    thread_id      text NOT NULL DEFAULT '',
    generation     integer NOT NULL DEFAULT 1,
    status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    last_active_at timestamptz NOT NULL DEFAULT now(),
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, chat_id, member_id, thread_id)
);

-- 消息 ---------------------------------------------------------------------

CREATE TABLE message (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    conversation_id      uuid NOT NULL REFERENCES conversation (id) ON DELETE CASCADE,
    direction            text NOT NULL CHECK (direction IN ('user', 'assistant', 'system')),
    external_id          text NOT NULL DEFAULT '',
    seq                  bigint NOT NULL,
    text                 text NOT NULL DEFAULT '',
    msg_type             text NOT NULL DEFAULT 'text',
    status               text NOT NULL DEFAULT 'received'
                         CHECK (status IN ('received', 'queued', 'processing', 'done', 'failed', 'cancelled', 'stopped')),
    sender_open_id       text NOT NULL DEFAULT '',
    reply_to_external_id text NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL DEFAULT now()
);

-- 接入去重：同一租户下外部消息 ID 唯一；机器人回复尚未发送时为空串，不参与唯一约束
CREATE UNIQUE INDEX message_external_id_idx
    ON message (tenant_id, external_id)
    WHERE external_id <> '';

CREATE INDEX message_conversation_seq_idx ON message (conversation_id, seq);
CREATE INDEX message_status_idx ON message (status) WHERE status IN ('queued', 'processing');

CREATE TABLE message_attachment (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id  uuid NOT NULL REFERENCES message (id) ON DELETE CASCADE,
    source      text NOT NULL DEFAULT 'main',
    kind        text NOT NULL,
    file_key    text NOT NULL DEFAULT '',
    name        text NOT NULL DEFAULT '',
    mime        text NOT NULL DEFAULT '',
    size_bytes  bigint NOT NULL DEFAULT 0,
    sha256      text NOT NULL DEFAULT '',
    storage_key text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'ready', 'failed')),
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- 运行 ---------------------------------------------------------------------

CREATE TABLE run (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    conversation_id   uuid NOT NULL REFERENCES conversation (id) ON DELETE CASCADE,
    generation        integer NOT NULL,
    state             text NOT NULL DEFAULT 'queued'
                      CHECK (state IN ('queued', 'running', 'stopping', 'completed', 'failed', 'interrupted')),
    reply_to_message  uuid REFERENCES message (id),
    input_count       integer NOT NULL DEFAULT 0,
    started_at        timestamptz,
    ended_at          timestamptz,
    end_reason        text NOT NULL DEFAULT '',
    error             text NOT NULL DEFAULT '',
    usage             jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- 同一会话最多一个非终态运行（不变量 I1）
CREATE UNIQUE INDEX run_one_active_per_conversation
    ON run (conversation_id)
    WHERE state IN ('queued', 'running', 'stopping');

CREATE TABLE run_input (
    run_id     uuid NOT NULL REFERENCES run (id) ON DELETE CASCADE,
    message_id uuid NOT NULL REFERENCES message (id) ON DELETE CASCADE,
    position   integer NOT NULL,
    PRIMARY KEY (run_id, message_id),
    UNIQUE (run_id, position)
);

-- 追加写、不可变
CREATE TABLE run_event (
    id         bigserial PRIMARY KEY,
    run_id     uuid NOT NULL REFERENCES run (id) ON DELETE CASCADE,
    kind       text NOT NULL,
    payload    jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX run_event_run_idx ON run_event (run_id, id);

CREATE TABLE tool_call (
    id         bigserial PRIMARY KEY,
    run_id     uuid NOT NULL REFERENCES run (id) ON DELETE CASCADE,
    name       text NOT NULL,
    arguments  jsonb NOT NULL DEFAULT '{}'::jsonb,
    result     jsonb NOT NULL DEFAULT '{}'::jsonb,
    status     text NOT NULL DEFAULT 'running'
               CHECK (status IN ('running', 'ok', 'error', 'timeout')),
    started_at timestamptz NOT NULL DEFAULT now(),
    ended_at   timestamptz
);

-- 投递 ---------------------------------------------------------------------

CREATE TABLE delivery (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    run_id         uuid NOT NULL REFERENCES run (id) ON DELETE CASCADE,
    kind           text NOT NULL CHECK (kind IN ('result', 'failure', 'interrupted', 'status')),
    idempotency_key text NOT NULL UNIQUE,
    state          text NOT NULL DEFAULT 'pending'
                   CHECK (state IN ('pending', 'sending', 'sent', 'retrying', 'failed', 'ambiguous')),
    reply_to       uuid REFERENCES message (id),
    target_type    text NOT NULL CHECK (target_type IN ('chat', 'user')),
    target_id      text NOT NULL,
    attempts       integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until    timestamptz,
    external_id    text NOT NULL DEFAULT '',
    last_error     text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    sent_at        timestamptz
);

CREATE INDEX delivery_due_idx ON delivery (next_attempt_at) WHERE state IN ('pending', 'retrying');

-- 投影 ---------------------------------------------------------------------

CREATE TABLE reaction_projection (
    message_id    uuid PRIMARY KEY REFERENCES message (id) ON DELETE CASCADE,
    desired_state text NOT NULL DEFAULT '',
    applied_state text NOT NULL DEFAULT '',
    attempts      integer NOT NULL DEFAULT 0,
    last_error    text NOT NULL DEFAULT '',
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- 用量、审计、记忆 ---------------------------------------------------------

CREATE TABLE usage_record (
    id            bigserial PRIMARY KEY,
    tenant_id     uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    run_id        uuid REFERENCES run (id) ON DELETE SET NULL,
    provider      text NOT NULL,
    model         text NOT NULL,
    input_tokens  integer NOT NULL DEFAULT 0,
    output_tokens integer NOT NULL DEFAULT 0,
    cost_micros   bigint NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_event (
    id         bigserial PRIMARY KEY,
    tenant_id  uuid REFERENCES tenant (id) ON DELETE CASCADE,
    actor      text NOT NULL DEFAULT '',
    action     text NOT NULL,
    target     text NOT NULL DEFAULT '',
    detail     jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE memory (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid NOT NULL REFERENCES tenant (id) ON DELETE CASCADE,
    scope      text NOT NULL CHECK (scope IN ('user', 'team')),
    owner_id   text NOT NULL DEFAULT '',
    content    text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX memory_scope_idx ON memory (tenant_id, scope, owner_id);
