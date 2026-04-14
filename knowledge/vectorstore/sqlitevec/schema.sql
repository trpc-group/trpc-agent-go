-- SQLite-Vec schema for knowledge/vectorstore/sqlitevec.
--
-- Current design:
-- 1. The vec0 main table intentionally stays close to pgvector's document schema.
-- 2. Extended document fields and filterable metadata are expanded into a separate
--    metadata index table maintained by application-layer transactions.
--
-- Replace:
--   {{VEC_TABLE_NAME}}  with the vec0 table name
--   {{META_TABLE_NAME}} with the metadata index table name
--   {{DIMENSION}}       with the embedding dimension

CREATE VIRTUAL TABLE IF NOT EXISTS {{VEC_TABLE_NAME}} USING vec0(
  id text primary key,
  embedding float[{{DIMENSION}}] distance_metric=cosine,
  created_at integer,
  updated_at integer,
  +name text,
  +content text,
  +metadata text
);

CREATE TABLE IF NOT EXISTS {{META_TABLE_NAME}} (
  doc_id TEXT NOT NULL,
  key TEXT NOT NULL,
  value_ordinal INTEGER NOT NULL DEFAULT 0,
  value_type TEXT NOT NULL,
  value_text TEXT,
  value_num REAL,
  value_bool INTEGER,
  value_json TEXT,
  PRIMARY KEY (doc_id, key, value_ordinal)
);

CREATE INDEX IF NOT EXISTS {{META_TABLE_NAME}}__key__value_text
ON {{META_TABLE_NAME}}(key, value_text);

CREATE INDEX IF NOT EXISTS {{META_TABLE_NAME}}__key__value_num
ON {{META_TABLE_NAME}}(key, value_num);

CREATE INDEX IF NOT EXISTS {{META_TABLE_NAME}}__key__value_bool
ON {{META_TABLE_NAME}}(key, value_bool);

CREATE INDEX IF NOT EXISTS {{META_TABLE_NAME}}__doc_id
ON {{META_TABLE_NAME}}(doc_id);

CREATE INDEX IF NOT EXISTS {{META_TABLE_NAME}}__doc_id__key
ON {{META_TABLE_NAME}}(doc_id, key);
