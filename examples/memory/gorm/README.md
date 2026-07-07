# GORM Memory Example

Interactive chat demo using [`memory/gorm`](../../../memory/gorm) with a **shared `*gorm.DB`**.

This matches how Guild/Genie wire episodic memory: the host application owns the database connection and (in production) the DDL.

## Quick start (SQLite)

```bash
cd examples/memory/gorm
export OPENAI_API_KEY="your-api-key"
go run .
```

By default the example:

1. Opens a local SQLite file (`memories_gorm.db`)
2. Runs GORM `AutoMigrate` for the `memories` table
3. Enables the four agentic memory tools (`memory_add`, `memory_update`, `memory_search`, `memory_load`)
4. Starts an interactive chat session

## PostgreSQL (host-owned DDL)

When memory lives in an existing application database, skip initialization and point at your DSN:

```bash
export GORM_DSN="postgres://user:pass@localhost:5432/app?sslmode=disable"
go run . -skip-db-init
```

Create the table using the reference DDL below.

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

### Column semantics

| Column | Description |
|--------|-------------|
| `memory_id` | Stable primary key (SHA-256 hex, 64 chars) |
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
| `WithTableName(name)` | Custom table name (default `memories`) |
| `WithSoftDelete(true)` | Soft delete via `deleted_at` |
| `WithMinSearchScore` / `WithMaxResults` | Keyword search tuning (matches `memory/postgres`) |
| `WithToolEnabled` / `WithExtractor` | Opt in to built-in memory tools and auto-memory mode |

By default no memory tools are registered (empty `Tools()`), which suits hosts that expose vector memory tools separately.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-sqlite` | `memories_gorm.db` | SQLite file when `GORM_DSN` is unset |
| `-skip-db-init` | `false` | Skip `AutoMigrate` (`WithSkipDBInit(true)`) |
| `-soft-delete` | `false` | Soft delete via `deleted_at` |
| `-model` | `deepseek-v4-flash` | Chat model |
| `-streaming` | `true` | Stream assistant output |

## Environment

| Variable | Description |
|----------|-------------|
| `OPENAI_API_KEY` | Required |
| `GORM_DSN` | Optional SQLite path or `postgres://` DSN |

## Wiring pattern

```go
db, _ := gorm.Open(sqlite.Open("memories.db"), &gorm.Config{})

memorySvc, err := memorygorm.NewService(db,
    memorygorm.WithSkipDBInit(true), // production: host runs DDL
    memorygorm.WithToolEnabled(memory.AddToolName, true),
    // ... other tools as needed
)
```

## Tests

```bash
go test ./...
```
