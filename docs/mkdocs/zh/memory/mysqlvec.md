# MySQL Vector（mysqlvec）存储

通用的 Agent 接入、提取模式和工具配置请参阅[使用与配置](usage.md)。

**适用场景**：生产环境、MySQL 向量相似度搜索

MySQL Vector 将记忆存储在 MySQL 中，通过 embedding 向量提供语义相似度搜索。
MySQL 9.0+ 使用原生 `VECTOR` 类型，旧版本自动降级为 `BLOB` 存储 + Go 侧余弦相似度计算。

**MySQL 版本要求**：

- **MySQL 5.7.8+**：支持（BLOB 降级路径，Go 侧暴力余弦相似度）
- **MySQL 8.x**：支持（BLOB 降级路径）
- **MySQL 9.0+**：完整支持，使用原生 VECTOR 类型进行数据库侧相似度搜索

```go
import memorymysqlvec "trpc.group/trpc-go/trpc-agent-go/memory/mysqlvec"
import openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

mysqlvecService, err := memorymysqlvec.NewService(
    memorymysqlvec.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
    memorymysqlvec.WithEmbedder(embedder),
    memorymysqlvec.WithSoftDelete(true),
)
```

**配置选项**：

- `WithMySQLClientDSN(dsn)`: MySQL DSN 连接字符串（推荐，必需 `parseTime=true`）
- `WithMySQLInstance(name)`: 使用预注册的 MySQL 实例
- `WithEmbedder(embedder)`: 文本嵌入器，用于生成向量（必需）
- `WithSoftDelete(enabled)`: 启用软删除（默认 false）
- `WithTableName(name)`: 自定义表名（默认 "memories"）
- `WithIndexDimension(dim)`: 向量维度（默认 1536）
- `WithMaxResults(limit)`: 最大搜索结果数（默认 15）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 MySQL 客户端的额外选项
- `WithSkipDBInit(skip)`: 跳过表初始化（适用于无 DDL 权限场景）

**注意**：需要 MySQL 5.7.8+（JSON 列类型）。MySQL 9.0+ 使用原生 VECTOR 支持；MySQL 5.7/8.x 自动降级为 BLOB + Go 侧余弦相似度。不需要额外的向量库。

**表结构**（自动创建，MySQL 9.0+）：

```sql
CREATE TABLE memories (
    memory_id VARCHAR(64) PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_content TEXT NOT NULL,
    topics JSON,
    embedding VECTOR(1536),
    memory_kind VARCHAR(32) NOT NULL DEFAULT 'fact',
    event_time TIMESTAMP(6) NULL,
    participants JSON,
    location VARCHAR(1024) NULL,
    created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at TIMESTAMP(6) NULL DEFAULT NULL,
    INDEX idx_app_user (app_name, user_id),
    INDEX idx_updated_at (updated_at DESC),
    INDEX idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

**资源清理**：使用完毕后需调用 `Close()` 方法释放数据库连接：

```go
defer mysqlvecService.Close()
```
