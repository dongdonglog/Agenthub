-- 0013_conversation_profile.sql — 会话代绑定配置档案

CREATE TABLE IF NOT EXISTS conversation_profile (
    conversation_id text NOT NULL,
    generation      integer NOT NULL,
    profile_name    text NOT NULL DEFAULT '',
    skills          jsonb NOT NULL DEFAULT '[]'::jsonb,
    mcps            jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, generation)
);
