# pgvector 存储

通用的 Agent 接入、提取模式和工具配置请参阅[使用与配置](usage.md)。

**适用场景**：生产环境、向量相似度搜索

请通过环境变量或密钥管理系统设置 `PGVECTOR_DSN`。生产 DSN 应校验服务端证书，
并使用证书覆盖的主机名：

```text
postgres://<user>:<password>@db.example.com:5432/dbname?sslmode=verify-full&sslrootcert=<trusted-ca-path>
```

```go
import (
    "os"

    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    memorypgvector "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"
)

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

pgvectorService, err := memorypgvector.NewService(
    memorypgvector.WithPGVectorClientDSN(os.Getenv("PGVECTOR_DSN")),
    memorypgvector.WithEmbedder(embedder),
    memorypgvector.WithSoftDelete(true),
)
if err != nil {
    panic(err)
}
```

**配置选项**：

- `WithPGVectorClientDSN(dsn)`: 推荐的连接方式，优先级最高
- `WithHost/WithPort/WithUser/WithPassword/WithDatabase`: 可选的分字段连接参数
- `WithSSLMode(mode)`: SSL 模式（默认 "disable"）
- `WithPostgresInstance(name)`: 使用预注册的 PostgreSQL 实例
- `WithEmbedder(embedder)`: 文本嵌入器，用于生成向量（必需）
- `WithSoftDelete(enabled)`: 启用软删除（默认 false）
- `WithTableName(name)`: 自定义表名（默认 "memories"）
- `WithSchema(schema)`: 指定数据库 schema（默认为 public）
- `WithIndexDimension(dim)`: 向量维度（默认 1536）
- `WithMaxResults(limit)`: 最大搜索结果数（默认 10）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 PostgreSQL 客户端的额外选项
- `WithSkipDBInit(skip)`: 跳过表初始化（适用于无 DDL 权限场景）
- `WithHNSWIndexParams(params)`: HNSW 索引参数，用于向量搜索

**注意**：DSN 优先于分字段连接参数，这两种直接连接方式都优先于
`WithPostgresInstance`。SSL mode 默认为 `disable`，只应在可信的本地开发环境
使用，不要用于生产环境。PostgreSQL 中必须安装 pgvector 扩展。

**默认初始化结构**：

service 会启用 `vector` 扩展，并在 `public.memories` 中初始化向量、事件元数据和
全文检索字段。`WithTableName`、`WithSchema`、`WithIndexDimension` 会分别替换
`memories`、`public`、`1536`。初始化还会为 app/user、更新时间、删除时间、
事件时间、kind、participants（GIN）、embedding（HNSW）和
`search_vector`（GIN）创建索引，并创建由 `memory_content` 维护
`search_vector` 的触发器。

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE public.memories (
    memory_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memory_content TEXT NOT NULL,
    topics TEXT[],
    embedding vector(1536),
    memory_kind TEXT NOT NULL DEFAULT 'fact',
    event_time TIMESTAMP NULL,
    participants TEXT[],
    location TEXT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    search_vector tsvector
);
```

`WithSkipDBInit(true)` 会跳过扩展、表、索引、触发器函数、触发器和全文字段回填。
启动 service 前必须预先完成这些 DDL；请以
[`memory/pgvector/init.go`](https://github.com/trpc-group/trpc-agent-go/blob/main/memory/pgvector/init.go)
为准，并同步自定义的 HNSW 参数。

**资源清理**：使用完毕后需调用 `Close()` 方法释放数据库连接：

```go
defer pgvectorService.Close()
```
