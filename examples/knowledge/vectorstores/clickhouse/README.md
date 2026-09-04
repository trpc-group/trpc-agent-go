# ClickHouse Vector Store Example

Demonstrates vector storage using ClickHouse. This example runs the full
`VectorStore` surface (Add/Get/Search/Update/Count/Delete) without requiring an
LLM or embedding service — only a running ClickHouse instance is needed.

## Prerequisites

Start ClickHouse:

```bash
docker run -d --name clickhouse \
  -p 9000:9000 \
  clickhouse/clickhouse-server:latest
```

Optionally override the connection DSN and table name (defaults are
`clickhouse://default:@localhost:9000/default` and
`clickhouse_vectorstore_example`):

```bash
export CLICKHOUSE_DSN=clickhouse://user:password@host:9000/database
export CLICKHOUSE_TABLE=clickhouse_vectorstore_example
```

> The example creates the table if it does not exist and reuses it otherwise. It
> upserts `doc1`, `doc2`, and `doc3`, updates `doc1`, then deletes `doc3`, so
> `doc1` and `doc2` remain in the table after a run. Point `CLICKHOUSE_TABLE` at
> a throwaway table if you do not want those rows written.

## Run

```bash
go run main.go
```

The example prints each step (table creation, inserting 3 documents, vector
search where `[1,0,0]` should rank `doc1` first, filter search, keyword search,
hybrid search, update, count, delete) and finishes with a verification summary.

## Features

- **Native protocol**: Connects over the ClickHouse native port (9000), not HTTP (8123)
- **Four search modes**: Vector, filter-only, keyword, and hybrid
- **Typed filter columns**: Fields declared via `WithFilterFields` are materialized as dedicated columns
- **Upsert semantics**: `ReplacingMergeTree(updated_at)` collapses older versions of the same document ID
