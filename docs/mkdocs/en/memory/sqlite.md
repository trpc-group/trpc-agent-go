# SQLite Storage

For common Agent integration, extraction modes, and tool configuration, see
[Usage and Configuration](usage.md).

**Use case**: Local persistence, single-node deployments, demos

SQLite stores data in a single file. It is useful when you want persistence
without operating MySQL/PostgreSQL/Redis.

```go
import (
    "database/sql"

    _ "github.com/mattn/go-sqlite3"
    memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
)

db, err := sql.Open("sqlite3", "file:memories.db?_busy_timeout=5000")
if err != nil {
    panic(err)
}

memoryService, err := memorysqlite.NewService(
    db,
    memorysqlite.WithSoftDelete(true),
    memorysqlite.WithMemoryLimit(200),
)
if err != nil {
    _ = db.Close()
    panic(err)
}
defer memoryService.Close()
```

**Configuration options**:

- `WithTableName(name)`: Table name (default "memories")
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithSkipDBInit(skip)`: Skip table initialization
- Auto mode: `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`, `WithMemoryJobTimeout`
- Tools: `WithCustomTool`, `WithToolEnabled`

**Notes**:

- This backend uses `github.com/mattn/go-sqlite3` and requires CGO.
- After `NewService` succeeds, the service owns the `*sql.DB` and closes it in
  `Close()`. The caller must close `db` if construction fails.
