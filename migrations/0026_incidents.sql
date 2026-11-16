-- 0026_incidents.sql — AIOps 告警事件与事故时间线

CREATE TABLE IF NOT EXISTS incident (
    id              text PRIMARY KEY,
    fingerprint     text NOT NULL,
    status          text NOT NULL DEFAULT 'firing',
    severity        text NOT NULL DEFAULT '',
    title           text NOT NULL DEFAULT '',
    summary         text NOT NULL DEFAULT '',
    service         text NOT NULL DEFAULT '',
    environment     text NOT NULL DEFAULT '',
    chat_id         text NOT NULL DEFAULT '',
    alert_count     integer NOT NULL DEFAULT 0,
    acknowledged_by text NOT NULL DEFAULT '',
    acknowledged_at timestamptz,
    resolved_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS incident_active_fingerprint_idx
    ON incident (fingerprint) WHERE status <> 'resolved';
CREATE INDEX IF NOT EXISTS incident_recent_idx ON incident (updated_at DESC);

CREATE TABLE IF NOT EXISTS alert_event (
    id          text PRIMARY KEY,
    incident_id text NOT NULL DEFAULT '',
    fingerprint text NOT NULL,
    status      text NOT NULL,
    alertname   text NOT NULL DEFAULT '',
    severity    text NOT NULL DEFAULT '',
    summary     text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    starts_at   timestamptz,
    ends_at     timestamptz,
    labels      jsonb NOT NULL DEFAULT '{}'::jsonb,
    annotations jsonb NOT NULL DEFAULT '{}'::jsonb,
    received_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS alert_event_dedupe_idx
    ON alert_event (fingerprint, status, starts_at);
CREATE INDEX IF NOT EXISTS alert_event_incident_idx ON alert_event (incident_id, received_at);

CREATE TABLE IF NOT EXISTS incident_event (
    id          bigserial PRIMARY KEY,
    incident_id text NOT NULL,
    kind        text NOT NULL,
    actor       text NOT NULL DEFAULT '',
    text        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS incident_event_incident_idx ON incident_event (incident_id, id);
