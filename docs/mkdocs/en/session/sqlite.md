# SQLite Storage

SQLite is an embedded database stored in a single file. It is a good fit for:

- Local development and demos (no external database needed)
- Single-node deployments that still want persistence across restarts
- Lightweight persistence for CLI tools or small services

## Requirements

This backend uses the `github.com/mattn/go-sqlite3` driver, which requires CGO
(a C compiler). Make sure your environment can build CGO code.

## Basic Configuration Example

```go
import (
    "database/sql"
    "time"

    _ "github.com/mattn/go-sqlite3"
    sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

db, err := sql.Open("sqlite3", "file:sessions.db?_busy_timeout=5000")
if err != nil {
    // handle error
}

sessionService, err := sessionsqlite.NewService(
    db,
    sessionsqlite.WithSessionEventLimit(1000),
    sessionsqlite.WithSessionTTL(30*time.Minute),
    sessionsqlite.WithSoftDelete(true),
)
if err != nil {
    // handle error
}
defer sessionService.Close()
```

**Notes**:

- `NewService` accepts a `*sql.DB`. The session service owns the DB and will
  close it in `Close()`. Do not close the DB twice.
- For better concurrency on a single machine, consider enabling WAL mode
  (e.g. `_journal_mode=WAL`) and setting `_busy_timeout` in your DSN.

## Configuration Options

- **TTL and cleanup**: `WithSessionTTL`, `WithAppStateTTL`, `WithUserStateTTL`,
  `WithCleanupInterval`
- **Retention**: `WithSessionEventLimit`
- **Persistence**: `WithEnableAsyncPersist`, `WithAsyncPersisterNum`
- **Soft delete**: `WithSoftDelete` (default is enabled)
- **Summaries**: `WithSummarizer`, `WithAsyncSummaryNum`, `WithSummaryQueueSize`,
  `WithSummaryJobTimeout`
- **Schema/DDL**: `WithSkipDBInit`, `WithTablePrefix`
- **Hooks**: `WithAppendEventHook`, `WithGetSessionHook`

## Use Cases

| Scenario | Recommended Configuration |
| --- | --- |
| Local development | Default configuration with WAL mode |
| Single-node production | Configure TTL and enable WAL mode |
| CLI tools | Minimal configuration, single DB file |
| Testing | In-memory SQLite (`:memory:`) for isolation |

## Notes

1. **CGO Required**: The SQLite driver requires CGO. Make sure your build environment has a C compiler.
2. **WAL Mode**: Enable WAL mode (`_journal_mode=WAL`) for better concurrency.
3. **Busy Timeout**: Set `_busy_timeout` in DSN to handle concurrent access gracefully.
4. **Single File**: All data stored in a single file, easy to backup and migrate.
