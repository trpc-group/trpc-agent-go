# GORM Memory Example

Reference wiring for [`memory/gorm`](../../../memory/gorm) with host-owned or example-owned database connections.

This matches how Guild/Genie wire episodic memory: production hosts inject a shared `*gorm.DB` and run DDL; the interactive examples open a local SQLite/Postgres DSN via `WithDialector` so `Close()` releases the connection.

## Quick start (interactive chat)

Use the shared `simple` or `auto` examples with `-memory=gorm`:

```bash
cd examples/memory/simple
export OPENAI_API_KEY="your-api-key"
go run main.go -memory=gorm
```

By default this:

1. Opens a local SQLite file (`memories_gorm.db`, or `GORM_DSN` when set)
2. Runs GORM `AutoMigrate` and table-scoped indexes for the `memories` table
3. Exposes the default agentic memory tools (`memory_add`, `memory_update`, `memory_search`, `memory_load`)
4. Starts an interactive chat session

For automatic background extraction, use `examples/memory/auto` with the same flag.

## PostgreSQL (host-owned DDL)

When memory lives in an existing application database, skip initialization and point at your DSN:

```bash
export GORM_DSN="postgres://user:pass@localhost:5432/app?sslmode=disable"
export GORM_SKIP_DB_INIT=true
cd examples/memory/simple
go run main.go -memory=gorm
```

Create the table using the reference DDL below (index names should use your table prefix when not using the default `memories` table).

## Supported dialects

- **SQLite** — used by unit tests and this example's default path (`AutoMigrate`).
- **PostgreSQL** — production target (JSONB `memory_data` when using reference DDL).
- **MySQL** — `AutoMigrate` uses portable column types (`char(64)` PK, `varchar` scopes, JSON `memory_data`).

When the host owns DDL (`WithSkipDBInit(true)`), align columns with the reference schema below.

## Reference DDL (PostgreSQL)

```sql
CREATE TABLE memories (
  memory_id CHAR(64) PRIMARY KEY,
  app_name VARCHAR(255) NOT NULL,
  user_id VARCHAR(255) NOT NULL,
  memory_data JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  deleted_at TIMESTAMPTZ
);

CREATE INDEX idx_memories_app_user ON memories (app_name, user_id);
CREATE INDEX idx_memories_updated_at ON memories (updated_at DESC);
CREATE INDEX idx_memories_deleted_at ON memories (deleted_at);
```

When using `WithTableName("custom_memories")`, use matching index names such as `idx_custom_memories_app_user` (the service creates `idx_<table>_*` indexes during `AutoMigrate`).

### Column semantics

| Column | Description |
|--------|-------------|
| `memory_id` | Primary key (SHA-256 hex, 64 chars). **Rotates** when `UpdateMemory` changes content or metadata that affects the hash; treat it as derived, not a durable external reference. |
| `app_name` | Application scope (from `memory.UserKey`) |
| `user_id` | User scope (from `memory.UserKey`) |
| `memory_data` | JSON document encoding a full `memory.Entry` (same shape as `memory/postgres`) |
| `created_at` | Row creation time |
| `updated_at` | Last update time (used for ordering reads) |
| `deleted_at` | Optional soft-delete timestamp when `WithSoftDelete(true)` |

## Options

| Option | Purpose |
|--------|---------|
| `WithSkipDBInit(true)` | Skip `AutoMigrate`; host runs reference DDL |
| `WithTableName(name)` | Custom table name (default `memories`); indexes are created as `idx_<table>_*` |
| `WithSoftDelete(true)` | Soft delete via `deleted_at` |
| `WithMinSearchScore` / `WithMaxResults` | Keyword search tuning (matches `memory/postgres`) |
| `WithToolEnabled` / `WithExtractor` | Opt in/out of built-in memory tools and auto-memory mode |

By default the four agentic tools (`memory_add`, `memory_update`, `memory_search`, `memory_load`) are enabled and exposed via `Tools()`. Use `WithToolEnabled` / `WithToolHidden` to trim the surface for hosts that expose vector memory separately.

## Environment (`simple` / `auto` examples)

| Variable | Description |
|----------|-------------|
| `OPENAI_API_KEY` | Required |
| `GORM_DSN` | Optional SQLite path or `postgres://` DSN (default SQLite file: `memories_gorm.db`) |
| `GORM_SKIP_DB_INIT` | Set to `true` or `1` to skip `AutoMigrate` (`WithSkipDBInit(true)`) |

Shared example flags: `-memory=gorm`, `-soft-delete`, `-model`, `-streaming` (see `examples/memory/simple/README.md`).

## Wiring pattern (shared DB injection)

```go
db, _ := gorm.Open(sqlite.Open("memories.db"), &gorm.Config{})

memorySvc, err := memorygorm.NewService(
    memorygorm.WithDB(db),
    memorygorm.WithSkipDBInit(true), // production: host runs DDL
    memorygorm.WithToolEnabled(memory.AddToolName, true),
    // ... other tools as needed
)
```

## Tests

Run package tests from the repo root:

```bash
go test ./memory/gorm/...
```
