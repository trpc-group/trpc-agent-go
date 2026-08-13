# MySQL 存储

通用的 Agent 接入、提取模式和工具配置请参阅[使用与配置](usage.md)。

**适用场景**：生产环境、需要 ACID 保证、复杂查询

```go
import memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"

dsn := "user:password@tcp(localhost:3306)/dbname?parseTime=true"
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN(dsn),
    memorymysql.WithSoftDelete(true),
)
if err != nil {
    panic(err)
}
```

**配置选项**：

- `WithMySQLClientDSN(dsn)`: MySQL DSN 连接字符串（推荐，必需
  `parseTime=true`）
- `WithMySQLInstance(name)`: 使用预注册的 MySQL 实例
- `WithSoftDelete(enabled)`: 启用软删除（默认 false）
- `WithTableName(name)`: 自定义表名（默认 "memories"）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 MySQL 客户端的额外选项
- `WithSkipDBInit(skip)`: 跳过表初始化（适用于无 DDL 权限场景）

同时设置时，`WithMySQLClientDSN` 的优先级高于 `WithMySQLInstance`。

**DSN 示例**：

```text
root:password@tcp(localhost:3306)/memory_db?parseTime=true&charset=utf8mb4
```

**默认表结构**（自动创建）：

`WithTableName` 会替换以下 DDL 中的 `memories`。

```sql
CREATE TABLE memories (
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_id VARCHAR(64) NOT NULL,
    memory_data JSON NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (app_name, user_id, memory_id),
    INDEX idx_app_user (app_name, user_id),
    INDEX idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
```

**资源清理**：使用完毕后需调用 `Close()` 方法释放数据库连接：

```go
defer mysqlService.Close()
```
