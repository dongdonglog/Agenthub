-- 0023_admin_intervention.sql — 管理员跨会话干预标记

ALTER TABLE message_state ADD COLUMN IF NOT EXISTS sent_by_admin boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS delivery_state_external_id_idx
    ON delivery_state (external_id) WHERE external_id <> '';
