# SQLite 存储

通用的 Agent 接入、提取模式和工具配置请参阅[使用与配置](usage.md)。

**适用场景**：本地持久化、单机部署、Demo

SQLite 将数据保存在单个文件中，适用于不想运维 MySQL/PostgreSQL/Redis
但希望进程重启后仍能保留记忆数据的场景。

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

**配置选项**：

- `WithTableName(name)`: 表名（默认 "memories"）
- `WithSoftDelete(enabled)`: 软删除（默认 false）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithSkipDBInit(skip)`: 跳过表初始化
- Auto 模式：`WithExtractor`、`WithAsyncMemoryNum`、`WithMemoryQueueSize`、`WithMemoryJobTimeout`
- 工具：`WithCustomTool`、`WithToolEnabled`

**注意事项**：

- 该后端使用 `github.com/mattn/go-sqlite3`，需要 CGO。
- `NewService` 成功后，service 会接管传入的 `*sql.DB` 并在 `Close()` 时关闭；
  如果构造失败，调用方必须自行关闭 `db`。
