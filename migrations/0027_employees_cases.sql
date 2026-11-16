-- 0027_employees_cases.sql — 数字员工与统一案件

CREATE TABLE IF NOT EXISTS employee (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    title       text NOT NULL DEFAULT '',
    role        text NOT NULL DEFAULT '',
    mission     text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT 'active',
    chat_id     text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS case_item (
    id           text PRIMARY KEY,
    employee_id  text NOT NULL DEFAULT '',
    type         text NOT NULL DEFAULT 'incident',
    source       text NOT NULL DEFAULT '',
    source_id    text NOT NULL DEFAULT '',
    title        text NOT NULL DEFAULT '',
    summary      text NOT NULL DEFAULT '',
    severity     text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'new',
    requester    text NOT NULL DEFAULT '',
    service      text NOT NULL DEFAULT '',
    environment  text NOT NULL DEFAULT '',
    chat_id      text NOT NULL DEFAULT '',
    sla_due_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    resolved_at  timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS case_item_source_idx ON case_item (source, source_id);
CREATE INDEX IF NOT EXISTS case_item_recent_idx ON case_item (updated_at DESC);
CREATE INDEX IF NOT EXISTS case_item_status_idx ON case_item (status, updated_at DESC);

CREATE TABLE IF NOT EXISTS case_event (
    id         bigserial PRIMARY KEY,
    case_id    text NOT NULL,
    kind       text NOT NULL,
    actor      text NOT NULL DEFAULT '',
    text       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS case_event_case_idx ON case_event (case_id, id);
