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

Create the table using the reference DDL in [`memory/gorm/README.md`](../../../memory/gorm/README.md).

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
