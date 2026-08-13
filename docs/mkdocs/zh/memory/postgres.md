# PostgreSQL 存储

通用的 Agent 接入、提取模式和工具配置请参阅[使用与配置](usage.md)。

**适用场景**：生产环境、需要 JSONB 高级特性

```go
import memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"

postgresService, err := memorypostgres.NewService(
    memorypostgres.WithHost("localhost"),
    memorypostgres.WithPort(5432),
    memorypostgres.WithUser("postgres"),
    memorypostgres.WithPassword("password"),
    memorypostgres.WithDatabase("dbname"),
    memorypostgres.WithSoftDelete(true),
)
```

**配置选项**：

- `WithHost/WithPort/WithUser/WithPassword/WithDatabase`: 连接参数
- `WithSSLMode(mode)`: SSL 模式（默认 "disable"）
- `WithPostgresInstance(name)`: 使用预注册的 PostgreSQL 实例
- `WithSoftDelete(enabled)`: 启用软删除（默认 false）
- `WithTableName(name)`: 自定义表名（默认 "memories"）
- `WithSchema(schema)`: 指定数据库 schema（默认为 public）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 PostgreSQL 客户端的额外选项
- `WithSkipDBInit(skip)`: 跳过表初始化（适用于无 DDL 权限场景）

**注意**：直接连接参数优先级高于 `WithPostgresInstance`

**表结构**（自动创建）：

```sql
CREATE TABLE memories (
    memory_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memory_data JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

-- 性能索引
CREATE INDEX IF NOT EXISTS memories_app_user ON memories(app_name, user_id);
CREATE INDEX IF NOT EXISTS memories_updated_at ON memories(updated_at DESC);
CREATE INDEX IF NOT EXISTS memories_deleted_at ON memories(deleted_at);
```

**资源清理**：使用完毕后需调用 `Close()` 方法释放数据库连接：

```go
defer postgresService.Close()
```
