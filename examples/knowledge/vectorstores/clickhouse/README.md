# ClickHouse Vector Store Example

Demonstrates vector storage using ClickHouse. This example runs the full
`VectorStore` surface (Add/Get/Search/Update/Count/Delete) without requiring an
LLM or embedding service — only a running ClickHouse instance is needed.

## Prerequisites

Start ClickHouse:

```bash
docker run -d --name clickhouse \
  -p 9000:9000 \
  -e CLICKHOUSE_USER=default \
  -e CLICKHOUSE_PASSWORD=agentgo \
  -e CLICKHOUSE_DB=default \
  clickhouse/clickhouse-server:latest
```

The credentials are required, not cosmetic: with neither `CLICKHOUSE_USER` nor
`CLICKHOUSE_PASSWORD` set, the image logs `disabling network access for user
'default'` and the example fails to connect with `Authentication failed`. The
password above is a throwaway value for local testing only.

Optionally override the connection DSN and table name (defaults are
`clickhouse://default:agentgo@localhost:9000/default` and
`clickhouse_vectorstore_example`, matching the command above):

```bash
export CLICKHOUSE_DSN=clickhouse://user:password@host:9000/database
export CLICKHOUSE_TABLE=clickhouse_vectorstore_example
```

> The example creates the table if it does not exist and reuses it otherwise. It
> upserts `doc1`, `doc2`, and `doc3`, updates `doc1`, deletes `doc3`, then deletes
> `doc2` through a store configured with `WithSynchronousMutations(false)`, so
> only `doc1` remains in the table after a run. Point `CLICKHOUSE_TABLE` at a
> throwaway table if you do not want that row written.

## Run

```bash
go run main.go
```

The example prints each step (table creation, inserting 3 documents, vector
search where `[1,0,0]` should rank `doc1` first, filter search, keyword search,
hybrid search, update, count, synchronous delete, asynchronous delete) and
finishes with a verification summary.

## Features

- **Native protocol**: Connects over the ClickHouse native port (9000), not HTTP (8123)
- **Four search modes**: Vector, filter-only, keyword, and hybrid
- **Typed filter columns**: Fields declared via `WithFilterFields` are materialized as dedicated columns
- **Upsert semantics**: `ReplacingMergeTree(updated_at)` collapses older versions of the same document ID
