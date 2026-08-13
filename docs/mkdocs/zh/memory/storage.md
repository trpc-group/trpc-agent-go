# 存储后端

## 内存存储（InMemory）

**适用场景**：开发、测试、快速原型

```go
import memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"

memoryService := memoryinmemory.NewMemoryService()
```

**配置选项**：

- `WithMemoryLimit(limit int)`: 设置每用户记忆数量上限
- `WithCustomTool(toolName, creator)`: 注册自定义工具实现
- `WithToolEnabled(toolName, enabled)`: 启用/禁用特定工具

**特点**：零配置，高性能，无持久化

## SQLite 存储

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
    // 处理错误
}

memoryService, err := memorysqlite.NewService(
    db,
    memorysqlite.WithSoftDelete(true),
    memorysqlite.WithMemoryLimit(200),
)
if err != nil {
    // 处理错误
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
- `NewService` 会在 `Close()` 时关闭传入的 `*sql.DB`。

## SQLiteVec（sqlite-vec）存储

**适用场景**：本地持久化 + 语义检索（单机）

SQLiteVec 将记忆保存在 SQLite 文件中，并通过 `sqlite-vec` 提供向量相似度
检索（语义检索）。相比普通 SQLite 后端，它需要配置 **embedder** 来为
记忆和查询生成 embedding。

```go
import (
    "database/sql"

    _ "github.com/mattn/go-sqlite3"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    memorysqlitevec "trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec"
)

db, err := sql.Open("sqlite3", "file:memories_vec.db?_busy_timeout=5000")
if err != nil {
    // 处理错误
}

emb := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
)

memoryService, err := memorysqlitevec.NewService(
    db,
    memorysqlitevec.WithEmbedder(emb),
    memorysqlitevec.WithSoftDelete(true),
    memorysqlitevec.WithMemoryLimit(200),
)
if err != nil {
    // 处理错误
}
defer memoryService.Close()
```

**配置选项**：

- `WithTableName(name)`: 表名（默认 "memories"）
- `WithEmbedder(embedder)`: 文本 embedder（必填）
- `WithIndexDimension(dim)`: 向量维度（默认与 embedder 维度一致）
- `WithMaxResults(limit)`: 搜索返回的最大条数（默认 10）
- `WithSoftDelete(enabled)`: 软删除（默认 false）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithSkipDBInit(skip)`: 跳过表初始化
- Auto 模式：`WithExtractor`、`WithAsyncMemoryNum`、`WithMemoryQueueSize`、
  `WithMemoryJobTimeout`
- 工具：`WithCustomTool`、`WithToolEnabled`

**注意事项**：

- 该后端使用 `github.com/mattn/go-sqlite3`，需要 CGO。
- `sqlite-vec` 扩展通过 Go 绑定在进程内编译与注册，运行时无需额外下载
  `.so/.dylib` 文件。

## Redis 存储

**适用场景**：生产环境、高并发、分布式部署

```go
import memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"

redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
)
```

**配置选项**：

- `WithRedisClientURL(url)`: Redis 连接 URL（推荐）
- `WithRedisInstance(name)`: 使用预注册的 Redis 实例
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithKeyPrefix(prefix)`: 设置 Redis key 前缀。设置后所有 key 都会以 `prefix:` 开头。例如 `prefix` 为 `"myapp"` 时，key `mem:{app:user}` 变为 `myapp:mem:{app:user}`。默认为空（无前缀）。适用于多环境或多服务共享同一 Redis 实例的场景
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 Redis 客户端的额外选项

**注意**：`WithRedisClientURL` 优先级高于 `WithRedisInstance`

**Redis ACL 要求**：`UpdateMemory` 使用服务端 Lua 脚本，以原子方式校验并
轮换记忆 ID。除脚本使用的 `HEXISTS`、`HSET`、`HDEL` 命令和对应记忆 key
访问权限外，ACL 用户还必须具有 `EVALSHA` 和 `EVAL` 权限；脚本尚未缓存时
需要 `EVAL`。Redis 重启或执行 `SCRIPT FLUSH` 后脚本缓存可能被清除，因此
不能只在预热阶段临时授予 `EVAL`。

**Key 前缀示例**：

```go
redisService, err := memoryredis.NewService(
    memoryredis.WithRedisClientURL("redis://localhost:6379"),
    memoryredis.WithKeyPrefix("prod"),
)
```

## MySQL 存储

**适用场景**：生产环境、需要 ACID 保证、复杂查询

```go
import memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"

dsn := "user:password@tcp(localhost:3306)/dbname?parseTime=true"
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN(dsn),
    memorymysql.WithSoftDelete(true),
)
```

**配置选项**：

- `WithMySQLClientDSN(dsn)`: MySQL DSN 连接字符串（推荐，必需 `parseTime=true`）
- `WithMySQLInstance(name)`: 使用预注册的 MySQL 实例
- `WithSoftDelete(enabled)`: 启用软删除（默认 false）
- `WithTableName(name)`: 自定义表名（默认 "memories"）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithCustomTool(toolName, creator)`: 注册自定义工具
- `WithToolEnabled(toolName, enabled)`: 启用/禁用工具
- `WithExtraOptions(...options)`: 传递给 MySQL 客户端的额外选项
- `WithSkipDBInit(skip)`: 跳过表初始化（适用于无 DDL 权限场景）

**DSN 示例**：

```
root:password@tcp(localhost:3306)/memory_db?parseTime=true&charset=utf8mb4
```

**表结构**（自动创建）：

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

## MySQL Vector（mysqlvec）存储

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

## PostgreSQL 存储

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

## pgvector 存储

**适用场景**：生产环境、向量相似度搜索

```go
import memorypgvector "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"
import openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

embedder := openaiembedder.New(openaiembedder.WithModel("text-embedding-3-small"))

pgvectorService, err := memorypgvector.NewService(
    memorypgvector.WithHost("localhost"),
    memorypgvector.WithPort(5432),
    memorypgvector.WithUser("postgres"),
    memorypgvector.WithPassword("password"),
    memorypgvector.WithDatabase("dbname"),
    memorypgvector.WithEmbedder(embedder),
    memorypgvector.WithSoftDelete(true),
)
```

**配置选项**：

- `WithHost/WithPort/WithUser/WithPassword/WithDatabase`: 连接参数
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

**注意**：直接连接参数优先级高于 `WithPostgresInstance`。需要 PostgreSQL 中安装 pgvector 扩展。

**表结构**（自动创建）：

```sql
CREATE TABLE memories (
    memory_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    memory_content TEXT NOT NULL,
    topics TEXT[],
    embedding vector(1536),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL
);

-- 性能索引
CREATE INDEX ON memories(app_name, user_id);
CREATE INDEX ON memories(updated_at DESC);
CREATE INDEX ON memories(deleted_at);
CREATE INDEX ON memories USING hnsw (embedding vector_cosine_ops);
```

**资源清理**：使用完毕后需调用 `Close()` 方法释放数据库连接：

```go
defer pgvectorService.Close()
```

## ChromaDB 存储

**适用场景**：自建 ChromaDB 或 Chroma Cloud，使用余弦语义检索和混合检索

```go
import (
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    memorychromadb "trpc.group/trpc-go/trpc-agent-go/memory/chromadb"
)

embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
)

chromaService, err := memorychromadb.NewService(
    memorychromadb.WithBaseURL("http://localhost:8000"),
    memorychromadb.WithCollectionName("memories"),
    memorychromadb.WithEmbedder(embedder),
    memorychromadb.WithSoftDelete(true),
)
if err != nil {
    // 处理错误
}
defer chromaService.Close()
```

这是 client-server 模式的 REST 适配器，不是嵌入式 Chroma 运行时。需要单独
启动 Chroma 服务，或让 `WithBaseURL` 指向远程部署或 Chroma Cloud。Embedding
由配置的 tRPC-Agent-Go `embedder.Embedder` 生成；适配器不会安装或调用 Chroma
服务端 embedding function。

使用 Chroma Cloud 时可配置 `WithAPIKey`，该值通过 `X-Chroma-Token` 发送；如果
没有显式设置 tenant 和 database，服务会通过 identity 接口解析唯一作用域。
Bearer 和自定义请求头认证分别使用 `WithBearerToken` 和 `WithHTTPHeaders`，
主要面向代理或自定义网关。使用自定义认证请求头时，必须显式指定 tenant 和
database。非 loopback 地址只要携带认证或任意自定义请求头，就必须使用 HTTPS。

**配置选项**：

- 连接：`WithBaseURL`、`WithAPIKey`、`WithBearerToken`、
  `WithHTTPHeaders`、`WithTenant`、`WithDatabase`、`WithHTTPClient`、
  `WithTimeout`
- Collection：`WithCollectionName`、`WithAutoCreateCollection`、
  `WithIndexDimension`、`WithEmbedder`
- 检索：`WithMaxResults`、`WithSimilarityThreshold`、
  `WithHybridCandidateLimit`
- 保留策略：`WithMemoryLimit`、`WithSoftDelete`
- Auto 模式和工具配置与其他 memory 后端一致。

适配器直接使用 ChromaDB REST API v2，不依赖第三方 SDK。Collection 必须只启用
一个 HNSW 或 SPANN 索引，且距离度量必须为 `cosine`。记录在同一个 collection
内通过 schema、应用和用户 metadata 隔离。每用户容量限制只在单个 Service 实例
内串行保证；多实例同时写同一用户时，应在上层使用分布式锁或 sticky routing。

还需注意以下运行约束：

- 更换 embedding 模型时，即使新旧模型维度相同，也必须使用新 collection，或
  对全部记录重新生成 embedding。
- `EventTime` 和检索时间边界必须能以有符号 64 位 Unix 纳秒表示，即位于 UTC
  1677-09-21 至 2262-04-11 之间；超出范围的值会在请求 ChromaDB 前被拒绝。
- `WithHybridCandidateLimit` 是本地关键词候选扫描的硬上限，与
  `WithMemoryLimit` 无关。
- Chroma 没有为本流程提供跨请求事务或分页 snapshot token，因此多 Service
  实例下的容量检查、ID 轮换和分页读取都是 best-effort。
- Chroma Cloud 当前说明的限制包括：collection 名称最多 128 字节、查询结果最多
  300 条、单次写入最多 300 条、每个 collection 并发读写各 10。适配器只说明
  这些服务限制，不会静默 clamp 用户配置。

## 后端对比与选择

| 特性         | InMemory | SQLite     | SQLiteVec | Redis  | MySQL    | MySQLVec  | PostgreSQL | pgvector | ChromaDB    |
| ------------ | -------- | ---------- | --------- | ------ | -------- | --------- | ---------- | -------- | ----------- |
| **持久化**   | ❌       | ✅         | ✅        | ✅     | ✅       | ✅        | ✅         | ✅       | ✅          |
| **分布式**   | ❌       | ❌         | ❌        | ✅     | ✅       | ✅        | ✅         | ✅       | ✅          |
| **事务**     | ❌       | ✅ ACID    | ✅ ACID   | 部分   | ✅ ACID  | ✅ ACID   | ✅ ACID    | ✅ ACID  | 尽力保证    |
| **查询**     | 简单     | SQL        | SQL+向量  | 中等   | SQL      | SQL+向量  | SQL        | SQL+向量 | 向量+本地   |
| **JSON**     | ❌       | 基础       | 基础      | 基础   | JSON     | JSON      | JSONB      | JSONB    | Metadata    |
| **性能**     | 极高     | 中高       | 中高      | 高     | 中高     | 中高      | 中高       | 中高     | 高          |
| **配置**     | 零配置   | 简单       | 中等      | 简单   | 中等     | 中等      | 中等       | 中等     | 中等        |
| **软删除**   | ❌       | ✅         | ✅        | ❌     | ✅       | ✅        | ✅         | ✅       | ✅          |
| **适用场景** | 开发测试 | 本地持久化 | 本地向量  | 高并发 | 企业应用 | MySQL 向量 | 高级特性   | 向量搜索 | 向量服务    |

**选择建议**：

```
开发/测试 → InMemory（零配置，快速启动）
本地持久化 → SQLite（单文件数据库，易部署）
本地向量检索 → SQLiteVec（单文件数据库 + embedding）
高并发读写 → Redis（内存级性能）
需要 ACID → MySQL/PostgreSQL（事务保证）
复杂 JSON → PostgreSQL（JSONB 索引和查询）
MySQL 向量检索 → MySQLVec（MySQL 9.0+ 相似度检索）
向量搜索 → pgvector（基于 embedding 的相似度搜索）
向量服务 → ChromaDB（基于 REST 的余弦与混合检索）
审计追踪 → MySQL/MySQLVec/PostgreSQL/pgvector/ChromaDB/SQLite/SQLiteVec（软删除支持）
```
