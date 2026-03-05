# SQLite 存储

SQLite 是一种嵌入式数据库，数据保存在单个文件中，适合：

- 本地开发和 Demo（不需要额外部署数据库）
- 单机部署但希望进程重启后仍保留会话数据
- 轻量级 CLI/小服务的持久化

## 依赖与构建要求

该后端使用 `github.com/mattn/go-sqlite3` 驱动，需要开启 CGO（需要 C 编译器）。

## 基础配置示例

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

**注意事项**：

- `NewService` 接收 `*sql.DB`。Session Service 会在 `Close()` 时关闭该 DB，避免重复关闭。
- 如果单机并发较高，可以考虑在 DSN 中开启 WAL（例如 `_journal_mode=WAL`）并设置 `_busy_timeout`。

## 配置选项

- TTL 与清理：`WithSessionTTL`、`WithAppStateTTL`、`WithUserStateTTL`、`WithCleanupInterval`
- 保留策略：`WithSessionEventLimit`
- 异步持久化：`WithEnableAsyncPersist`、`WithAsyncPersisterNum`
- 软删除：`WithSoftDelete`（默认开启）
- 摘要：`WithSummarizer`、`WithAsyncSummaryNum`、`WithSummaryQueueSize`、`WithSummaryJobTimeout`
- DDL/命名：`WithSkipDBInit`、`WithTablePrefix`
- Hooks：`WithAppendEventHook`、`WithGetSessionHook`

## 使用场景

| 场景 | 推荐配置 |
| --- | --- |
| 本地开发 | 默认配置，可开启 WAL 模式 |
| 单机生产环境 | 配置 TTL，开启 WAL 模式 |
| CLI 工具 | 最简配置，单 DB 文件 |
| 测试环境 | 内存 SQLite（`:memory:`）实现隔离 |

## 注意事项

1. **CGO 要求**：SQLite 驱动需要 CGO，确保构建环境有 C 编译器
2. **WAL 模式**：建议开启 WAL 模式（`_journal_mode=WAL`）以获得更好并发性能
3. **Busy Timeout**：在 DSN 中设置 `_busy_timeout` 以优雅处理并发访问
4. **单文件**：所有数据保存在单个文件，便于备份和迁移
