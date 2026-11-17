-- 0033_case_sla_csat.sql — 支持 SLA/CSAT

ALTER TABLE case_feedback ADD COLUMN IF NOT EXISTS csat int NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS case_item_sla_idx ON case_item (sla_due_at) WHERE status NOT IN ('resolved','closed');
