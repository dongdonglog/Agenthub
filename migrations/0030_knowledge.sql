-- 0030_knowledge.sql — 技术支持知识库文档

CREATE TABLE IF NOT EXISTS knowledge_doc (
    id         text PRIMARY KEY,
    title      text NOT NULL DEFAULT '',
    content    text NOT NULL DEFAULT '',
    source     text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS knowledge_doc_updated_idx ON knowledge_doc (updated_at DESC);
CREATE INDEX IF NOT EXISTS knowledge_doc_title_idx ON knowledge_doc (lower(title));
