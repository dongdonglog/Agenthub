-- 0028_case_feedback.sql — 案件反馈与员工绩效

CREATE TABLE IF NOT EXISTS case_feedback (
    id         bigserial PRIMARY KEY,
    case_id    text NOT NULL,
    actor      text NOT NULL DEFAULT '',
    verdict    text NOT NULL,
    note       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS case_feedback_case_idx ON case_feedback (case_id, id);
CREATE INDEX IF NOT EXISTS case_feedback_verdict_idx ON case_feedback (verdict, created_at);
