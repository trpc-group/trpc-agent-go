# PGVector 会话存储

> **示例代码**: [examples/session/simple](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/simple)

PGVector 会话存储基于 `session/postgres` 扩展，在常规 PostgreSQL Session 持久化能力之上，增加了基于 pgvector 的语义召回能力。它适用于既需要持久化会话历史，又需要按“语义”检索历史对话内容的场景。

这里仍然保留 `session/postgres` 的常规能力：TTL 清理、软删除、摘要、Hook、Schema/表前缀、异步持久化等。PGVector 额外增加了事件 embedding、HNSW 索引，以及面向会话事件的搜索 API。

## 特点

- ✅ 数据持久化
- ✅ 支持分布式
- ✅ 支持跨会话语义召回
- ✅ 支持混合召回（`dense` + PostgreSQL 全文检索分支）
- ✅ 支持软删除
- ✅ 支持 Schema 和表前缀
- ✅ 支持异步持久化
- ✅ 支持摘要和 Hook

## 配置选项

### 连接配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithPostgresClientDSN(dsn string)` | `string` | - | PostgreSQL DSN，格式：`postgres://user:password@localhost:5432/dbname?sslmode=disable`（优先级最高） |
| `WithHost(host string)` | `string` | `localhost` | PostgreSQL 服务器地址 |
| `WithPort(port int)` | `int` | `5432` | PostgreSQL 服务器端口 |
| `WithUser(user string)` | `string` | `""` | 数据库用户名 |
| `WithPassword(password string)` | `string` | `""` | 数据库密码 |
| `WithDatabase(database string)` | `string` | `trpc-agent-go-pgsession` | 数据库名称 |
| `WithSSLMode(sslMode string)` | `string` | `disable` | SSL 模式，可选：`disable`、`require`、`verify-ca`、`verify-full` |
| `WithPostgresInstance(name string)` | `string` | - | 使用预配置的 PostgreSQL 实例（优先级最低） |
| `WithExtraOptions(extraOptions ...any)` | `[]any` | `nil` | 为 PostgreSQL 客户端设置额外选项 |

**优先级**：`WithPostgresClientDSN` > `WithHost/Port/User/Password/Database` > `WithPostgresInstance`

### 会话配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | 每个会话最大事件数量 |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 会话 TTL |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 应用状态 TTL |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 用户状态 TTL |
| `WithCleanupInterval(interval time.Duration)` | `time.Duration` | `0`（自动确定） | TTL 清理间隔；只要配置了任意 TTL，默认会回落到 5 分钟 |
| `WithSoftDelete(enable bool)` | `bool` | `true` | 启用或禁用软删除 |

### 异步持久化配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithEnableAsyncPersist(enable bool)` | `bool` | `false` | 为会话事件和 Track 事件启用异步持久化 |
| `WithAsyncPersisterNum(num int)` | `int` | `10` | 异步持久化 worker 数量 |

### 摘要配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | 注入会话摘要器 |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | 摘要处理 worker 数量 |
| `WithSummaryQueueSize(size int)` | `int` | `100` | 摘要任务队列大小 |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | 单个摘要任务超时时间 |

### Schema 和表配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithSchema(schema string)` | `string` | `""`（默认 schema） | 指定 schema 名称 |
| `WithTablePrefix(prefix string)` | `string` | `""` | 表名前缀 |
| `WithSkipDBInit(skip bool)` | `bool` | `false` | 跳过自动初始化数据库结构 |

### Hook 配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithAppendEventHook(hooks ...session.AppendEventHook)` | `[]session.AppendEventHook` | `nil` | 添加事件写入 Hook |
| `WithGetSessionHook(hooks ...session.GetSessionHook)` | `[]session.GetSessionHook` | `nil` | 添加会话读取 Hook |

### 向量与检索配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithEmbedder(e embedder.Embedder)` | `embedder.Embedder` | 必填 | 用于事件 embedding 和查询 embedding 的嵌入器 |
| `WithIndexDimension(dim int)` | `int` | `1536` | 向量维度，应与 embedder 保持一致 |
| `WithEmbedTimeout(timeout time.Duration)` | `time.Duration` | `30s` | embedding API 调用超时 |
| `WithSyncIndexing(sync bool)` | `bool` | `false` | 在事件落库后同步生成 embedding |
| `WithIndexTextBuilder(builder IndexTextBuilder)` | `IndexTextBuilder` | `nil` | 自定义写入 `content_text` 并参与 embedding 的文本 |
| `WithMaxResults(n int)` | `int` | `5` | `SearchEvents` 的默认返回数量 |
| `WithHNSWM(m int)` | `int` | `16` | HNSW 索引的 `m` 参数 |
| `WithHNSWEfConstruction(ef int)` | `int` | `200` | HNSW 索引的 `ef_construction` 参数 |
| `WithHybridRRFK(k int)` | `int` | `60` | 混合检索时的 RRF 常数 |
| `WithHybridCandidateRatio(ratio int)` | `int` | `3` | 混合检索每个分支的候选倍率 |

## 基础配置示例

```go
import (
    "time"

    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    sessionpgvector "trpc.group/trpc-go/trpc-agent-go/session/pgvector"
)

embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
)

sessionService, err := sessionpgvector.NewService(
    sessionpgvector.WithPostgresClientDSN(
        "postgres://user:password@localhost:5432/trpc_sessions?sslmode=disable",
    ),
    sessionpgvector.WithEmbedder(embedder),
    sessionpgvector.WithIndexDimension(embedder.GetDimensions()),
    sessionpgvector.WithSessionTTL(30*time.Minute),
    sessionpgvector.WithSoftDelete(true),
)
if err != nil {
    return err
}
defer sessionService.Close()
```

## 配置复用

```go
import (
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    sessionpg "trpc.group/trpc-go/trpc-agent-go/session/pgvector"
    "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

postgres.RegisterPostgresInstance("my-postgres-instance",
    postgres.WithClientConnString(
        "postgres://user:password@localhost:5432/trpc_sessions?sslmode=disable",
    ),
)

embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
)

sessionService, err := sessionpg.NewService(
    sessionpg.WithPostgresInstance("my-postgres-instance"),
    sessionpg.WithEmbedder(embedder),
    sessionpg.WithIndexDimension(embedder.GetDimensions()),
)
```

## 语义召回

`*pgvector.Service` 额外实现了 `session.SearchableService`，因此服务创建完成后可以直接做历史消息检索：

```go
import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/session"
)

ctx := context.Background()

results, err := sessionService.SearchEvents(ctx, session.EventSearchRequest{
    Query: "travel plan",
    UserKey: session.UserKey{
        AppName: "my-agent",
        UserID:  "user123",
    },
    SearchMode: session.SearchModeHybrid,
    MaxResults: 5,
})
if err != nil {
    return err
}

for _, hit := range results {
    fmt.Printf("[%0.3f] %s %s\n", hit.Score, hit.SessionKey.SessionID, hit.Text)
}
```

### 检索请求字段

| 字段 | 说明 |
| --- | --- |
| `Query` | 必填，语义检索查询文本 |
| `UserKey` | 必填，检索范围：`<appName, userID>` |
| `SessionIDs` | 只在指定的 session ID 集合中检索 |
| `ExcludeSessionIDs` | 从检索范围中排除指定 session ID |
| `Roles` | 只匹配指定消息角色 |
| `CreatedAfter` / `CreatedBefore` | 按事件时间范围过滤 |
| `FilterKey` | 按层级 branch/filter key 过滤事件 |
| `MaxResults` | 覆盖后端默认返回数量 |
| `MinScore` | Dense 相似度阈值；更适合与 `SearchModeDense` 搭配 |
| `SearchMode` | `session.SearchModeDense`（默认）或 `session.SearchModeHybrid` |
| `HybridRRFK` | 覆盖混合检索的默认 RRF 常数 |
| `HybridCandidateRatio` | 覆盖混合检索每个分支的候选倍率 |

**检索模式**

- `SearchModeDense`：只使用向量相似度
- `SearchModeHybrid`：向量召回 + PostgreSQL 全文检索，并使用 RRF 进行融合排序

### LLMAgent 自动 Recall 预加载

由于 `*pgvector.Service` 实现了 `session.SearchableService`，LLMAgent 可以直接把
当前用户消息当作 recall query，在模型调用前把其他会话里命中的历史事件注入到
system prompt：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session"
)

assistant := llmagent.New(
    "assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithPreloadSessionRecall(5),
    llmagent.WithPreloadSessionRecallMinScore(0.70),
    llmagent.WithPreloadSessionRecallSearchMode(session.SearchModeHybrid),
)

r := runner.NewRunner("my-app", assistant,
    runner.WithSessionService(sessionService),
)
```

说明：

- 检索范围限定在同一用户下，并且会自动排除当前会话，因此只会搜索**其他 session**。
- 召回片段会**合并到 system message**，并被标记为不可信的历史数据。
- 如果当前请求没有可用文本 query、后端没有命中结果，或子流程使用了 `include_contents="none"`，则会跳过 recall preload。
- 如果你需要显式控制 `SessionIDs`、`Roles`、时间范围等过滤条件，请直接调用 `SearchEvents`。

## 索引行为

PGVector 会在事件落库之后继续建立检索索引：

- 只有已持久化的用户/助手文本事件会被索引
- 工具调用、工具结果、partial 事件和空内容不会进入索引
- 默认在写入成功后异步生成 embedding
- 如果希望刚追加的事件立刻可检索，可启用 `WithSyncIndexing(true)`
- embedding 生成失败不会回滚主写入，只会记录 warning 日志
- `WithIndexTextBuilder(...)` 可以在 embedding 之前对写入 `content_text` 的文本做补充或规范化

因此，默认语义召回是**最终一致**的，而不是严格实时一致。

## 数据库初始化

除非显式开启 `WithSkipDBInit(true)`，服务会自动初始化 PostgreSQL：

1. 使用 `CREATE EXTENSION IF NOT EXISTS vector` 启用 `pgvector`
2. 创建与 `session/postgres` 相同的核心表
3. 为 `session_events` 补充向量检索相关字段
4. 创建 GIN 索引用于全文检索分支，创建 HNSW 索引用于向量召回

如果 HNSW 索引创建失败，服务只会打印 warning 并继续启动。此时 dense 检索仍然可以工作，但可能退化为更慢的查询计划，具体取决于 PostgreSQL/pgvector 环境。

如果开启 `WithSkipDBInit(true)`，请确保扩展、表、列和索引都已存在，并且 `embedding` 列维度与 `WithIndexDimension(...)` 完全一致。

## 存储结构

常规 Session 表结构与 `session/postgres` 保持一致。PGVector 额外扩展了 `session_events`：

| 列名 | 类型 | 作用 |
| --- | --- | --- |
| `content_text` | `TEXT` | 用于检索和返回的索引文本 |
| `role` | `VARCHAR(32)` | 归一化后的角色，便于过滤和展示 |
| `embedding` | `vector(N)` | 用于 dense 召回的事件向量 |
| `search_vector` | `tsvector` | PostgreSQL 文本检索向量，用于混合检索中的关键词分支 |

附加索引：

- `GIN(search_vector)`：服务 PostgreSQL 全文检索分支
- `HNSW(embedding vector_cosine_ops)`：服务向量相似度召回

## 使用场景

| 场景 | 推荐配置 |
| --- | --- |
| 持久化聊天历史并支持语义检索 | 默认配置 + `WithEmbedder(...)` |
| 同一用户的跨会话历史召回 | 使用 `SearchEvents` 并选择 `SearchModeHybrid` |
| 优先降低写入延迟 | 保持默认异步索引 |
| 需要追加后立刻可检索 | 启用 `WithSyncIndexing(true)` |
| 多租户 PostgreSQL | 配置 `WithSchema(...)` 和/或 `WithTablePrefix(...)` |

## 注意事项

1. **必须配置 Embedder**：未提供 `WithEmbedder(...)` 时，`NewService()` 会直接返回错误。
2. **维度必须匹配**：如果 embedder 能返回维度信息，就必须与 `WithIndexDimension(...)` 一致。
3. **全文检索语言**：内置 PostgreSQL 文本检索分支使用 `to_tsvector('english', content_text)`。对非英文内容，dense 检索仍然可用，但 hybrid 中的关键词分支行为会遵循 PostgreSQL 的 `english` 文本检索配置。
4. **权限要求**：自动初始化需要具备创建扩展、建表和建索引权限。
5. **资源释放**：服务不用时请调用 `Close()` 释放连接。
