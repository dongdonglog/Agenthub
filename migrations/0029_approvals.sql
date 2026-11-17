-- 0029_approvals.sql — 受控动作审批

CREATE TABLE IF NOT EXISTS approval_request (
    id           text PRIMARY KEY,
    case_id      text NOT NULL DEFAULT '',
    action       text NOT NULL,
    params       jsonb NOT NULL DEFAULT '{}'::jsonb,
    status       text NOT NULL DEFAULT 'pending',
    requested_by text NOT NULL DEFAULT '',
    approved_by  text NOT NULL DEFAULT '',
    result       text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS approval_case_idx ON approval_request (case_id, created_at DESC);
CREATE INDEX IF NOT EXISTS approval_status_idx ON approval_request (status, created_at DESC);
