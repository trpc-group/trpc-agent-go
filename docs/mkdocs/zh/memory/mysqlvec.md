# MySQL Vector（mysqlvec）存储

通用的 Agent 接入、提取模式和工具配置请参阅[使用与配置](usage.md)。

**适用场景**：生产环境、MySQL 向量相似度搜索

MySQL Vector 将记忆存储在 MySQL 中，通过 embedding 向量提供语义相似度搜索。
服务会在运行时探测原生 `VECTOR` 支持，否则自动降级为 `BLOB` 存储 + Go 侧
余弦相似度计算。生产环境使用原生向量时，请选择当前仍受支持的 MySQL 9.x 版本。

**MySQL 版本要求**：

- **MySQL 5.7.8+**：支持（BLOB 降级路径，Go 侧暴力余弦相似度）
- **MySQL 8.x**：支持（BLOB 降级路径）
- **支持原生 VECTOR 的 MySQL 9.x**：使用数据库侧相似度搜索

```go
import memorymysqlvec "trpc.group/trpc-go/trpc-agent-go/memory/mysqlvec"
import openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

mysqlvecService, err := memorymysqlvec.NewService(
    memorymysqlvec.WithMySQLClientDSN("user:password@tcp(localhost:3306)/dbname?parseTime=true"),
    memorymysqlvec.WithEmbedder(embedder),
    memorymysqlvec.WithSoftDelete(true),
)
if err != nil {
    panic(err)
}
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

同时设置时，`WithMySQLClientDSN` 的优先级高于 `WithMySQLInstance`。

**注意**：需要 MySQL 5.7.8+（JSON 列类型）。服务会探测原生 `VECTOR`
支持，探测失败时自动降级为 BLOB + Go 侧余弦相似度。不需要额外的向量库。

**默认表结构**（探测到原生 `VECTOR` 时自动创建）：

`WithTableName` 会替换 `memories`，`WithIndexDimension` 会替换 `1536`。在降级
路径中，`embedding VECTOR(1536) NOT NULL` 会变为 `embedding BLOB NOT NULL`，
其他结构相同。

```sql
CREATE TABLE memories (
    memory_id VARCHAR(64) PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    memory_content TEXT NOT NULL,
    topics JSON,
    embedding VECTOR(1536) NOT NULL,
    memory_kind VARCHAR(32) NOT NULL DEFAULT 'fact',
    event_time TIMESTAMP(6) NULL,
    participants JSON,
    location VARCHAR(1024) NULL,
    created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at TIMESTAMP(6) NULL DEFAULT NULL,
    FULLTEXT INDEX idx_fulltext (memory_content),
    INDEX idx_app_user (app_name, user_id),
    INDEX idx_updated_at (updated_at DESC),
    INDEX idx_deleted_at (deleted_at),
    INDEX idx_event_time (event_time DESC),
    INDEX idx_kind (app_name, user_id, memory_kind)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

**资源清理**：使用完毕后需调用 `Close()` 方法释放数据库连接：

```go
defer mysqlvecService.Close()
```
