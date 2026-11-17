-- 0034_case_reviews.sql — 人工评审五维评分

CREATE TABLE IF NOT EXISTS case_review (
    id          bigserial PRIMARY KEY,
    case_id     text NOT NULL,
    reviewer    text NOT NULL DEFAULT 'admin',
    evidence    smallint NOT NULL DEFAULT 0,
    root_cause  smallint NOT NULL DEFAULT 0,
    safety      smallint NOT NULL DEFAULT 0,
    usefulness  smallint NOT NULL DEFAULT 0,
    honesty     smallint NOT NULL DEFAULT 0,
    note        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (case_id, reviewer)
);

CREATE INDEX IF NOT EXISTS case_review_case_idx ON case_review (case_id);
