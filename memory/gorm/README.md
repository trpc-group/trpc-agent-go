# GORM memory backend

`memory/gorm` implements [`memory.Service`](../memory.go) using a shared [`gorm.io/gorm`](https://gorm.io) connection. Use it when episodic memory lives in the same database as the host application (for example Guild/Genie Postgres).

## Quick start

```go
import memorygorm "trpc.group/trpc-go/trpc-agent-go/memory/gorm"

svc, err := memorygorm.NewService(db, memorygorm.WithSkipDBInit(true))
if err != nil {
    return err
}
defer svc.Close()
```

Pass `WithSkipDBInit(true)` when the host application owns DDL (recommended for production).

## Reference DDL (PostgreSQL)

```sql
CREATE TABLE memories (
  memory_id TEXT PRIMARY KEY,
  app_name TEXT NOT NULL,
  user_id TEXT NOT NULL,
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
| `memory_id` | Stable primary key for the memory row |
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
| `WithToolEnabled` / `WithExtractor` | Opt in to built-in memory tools and auto-memory mode |

By default no memory tools are registered (empty `Tools()`), which suits hosts that expose vector memory tools separately.

## Development / tests

When `WithSkipDBInit` is false (default), the service runs GORM `AutoMigrate` on the memories table. SQLite is supported for unit tests.

## Example

See [`examples/memory/gorm`](../../examples/memory/gorm) for an interactive chat demo and a minimal shared-DB wiring pattern.
