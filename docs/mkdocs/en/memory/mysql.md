# MySQL Storage

For common Agent integration, extraction modes, and tool configuration, see
[Usage and Configuration](usage.md).

**Use case**: Production, ACID guarantees, complex queries

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

**Configuration options**:

- `WithMySQLClientDSN(dsn)`: MySQL DSN connection string (recommended;
  requires `parseTime=true`)
- `WithMySQLInstance(name)`: Use pre-registered MySQL instance
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithTableName(name)`: Custom table name (default "memories")
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithCustomTool(toolName, creator)`: Register custom tool
- `WithToolEnabled(toolName, enabled)`: Enable/disable tool
- `WithExtraOptions(...options)`: Extra options passed to MySQL client
- `WithSkipDBInit(skip)`: Skip table initialization (for users without DDL
  permissions)

`WithMySQLClientDSN` takes priority over `WithMySQLInstance` when both are set.

**DSN example**:

```text
root:password@tcp(localhost:3306)/memory_db?parseTime=true&charset=utf8mb4
```

**Default table schema** (auto-created):

`WithTableName` replaces `memories` in this DDL.

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

**Resource cleanup**: Call `Close()` method to release database connection:

```go
defer mysqlService.Close()
```
