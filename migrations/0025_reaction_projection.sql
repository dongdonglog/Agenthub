-- 0025_reaction_projection.sql — 飞书消息状态表情投影

CREATE TABLE IF NOT EXISTS reaction_projection (
    message_id  text PRIMARY KEY,
    state       text NOT NULL DEFAULT '',
    reaction_id text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now()
);
