-- 0031_knowledge_team.sql — 知识库按团队隔离

ALTER TABLE knowledge_doc ADD COLUMN IF NOT EXISTS team text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS knowledge_doc_team_idx ON knowledge_doc (team, updated_at DESC);
