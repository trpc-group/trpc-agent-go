# PostgreSQL 存储

通用的 Agent 接入、提取模式和工具配置请参阅[使用与配置](usage.md)。

**适用场景**：生产环境、需要 JSONB 高级特性

请通过环境变量或密钥管理系统设置 `POSTGRES_DSN`。生产 DSN 应校验服务端证书，
并使用证书覆盖的主机名：

```text
postgres://<user>:<password>@db.example.com:5432/dbname?sslmode=verify-full&sslrootcert=<trusted-ca-path>
```

```go
import (
    "os"

    memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
)

postgresService, err := memorypostgres.NewService(
    memorypostgres.WithPostgresClientDSN(os.Getenv("POSTGRES_DSN")),
    memorypostgres.WithSoftDelete(true),
)
if err != nil {
    panic(err)
}
```

**配置选项**：

- `WithPostgresClientDSN(dsn)`: 推荐的连接方式，优先级最高
- `WithHost/WithPort/WithUser/WithPassword/WithDatabase`: 可选的分字段连接参数
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

**注意**：DSN 优先于分字段连接参数，这两种直接连接方式都优先于
`WithPostgresInstance`。SSL mode 默认为 `disable`，只应在可信的本地开发环境
使用，不要用于生产环境。

**注册实例示例**：

```go
import (
    "os"

    memorypostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
    storagepostgres "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

storagepostgres.RegisterPostgresInstance(
    "my-postgres",
    storagepostgres.WithClientConnString(os.Getenv("POSTGRES_DSN")),
)
postgresService, err := memorypostgres.NewService(
    memorypostgres.WithPostgresInstance("my-postgres"),
)
if err != nil {
    panic(err)
}
```

**默认表结构**（自动创建于 `public.memories`）：

`WithSchema` 和 `WithTableName` 会替换以下 DDL 及索引语句中的 schema 和表名。

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
