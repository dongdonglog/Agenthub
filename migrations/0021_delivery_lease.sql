-- 0021_delivery_lease.sql — 投递租约与 claimed/calling 阶段

ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS lease_owner text NOT NULL DEFAULT '';
ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS lease_token text NOT NULL DEFAULT '';
ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS lease_expires_at timestamptz;
ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS send_phase text NOT NULL DEFAULT '';
ALTER TABLE delivery_state ADD COLUMN IF NOT EXISTS external_id text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS delivery_state_lease_idx
    ON delivery_state (state, lease_expires_at);
