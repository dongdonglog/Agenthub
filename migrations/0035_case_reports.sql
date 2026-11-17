-- 0035_case_reports.sql — 处置报告

CREATE TABLE IF NOT EXISTS case_report (
    case_id     text PRIMARY KEY,
    problem     text NOT NULL DEFAULT '',
    impact      text NOT NULL DEFAULT '',
    evidence    text NOT NULL DEFAULT '',
    root_cause  text NOT NULL DEFAULT '',
    actions     text NOT NULL DEFAULT '',
    verification text NOT NULL DEFAULT '',
    prevention  text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT 'draft',
    author      text NOT NULL DEFAULT 'admin',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS case_report_status_idx ON case_report (status, updated_at DESC);
