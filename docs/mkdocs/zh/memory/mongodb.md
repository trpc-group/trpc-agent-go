# MongoDB 存储

通用的 Agent 接入、提取模式和工具配置请参阅[使用与配置](usage.md)。

## 概述

MongoDB 后端（`memory/mongodb`）基于 MongoDB 实现 `memory.Service`，是框架中第一个
MongoDB memory 后端。它复用 `storage/mongodb` 连接层，与其他基于 MongoDB 的组件
共享同一套 client builder 和实例注册机制。

**适用场景**：生产环境、文档型存储、灵活 schema

**特性**：

- 持久化文档存储，一条记忆对应一个文档
- 可选软删除（tombstone），重新添加时自动复活
- 幂等 Add 与 Update 的规范 ID 轮转，语义与其他关系型后端一致
- 基于 `<appName, userID>` 的作用域隔离，配套专用索引
- 应用层 BM25 风格关键词搜索，与 `mysql` / `postgres` 后端一致
- 可在单机版 MongoDB 部署上运行（不依赖多文档事务）

## 使用方式

直接使用 MongoDB URI 连接（推荐）：

```go
import memorymongodb "trpc.group/trpc-go/trpc-agent-go/memory/mongodb"

mongodbService, err := memorymongodb.NewService(
    memorymongodb.WithMongoClientURI("mongodb://localhost:27017"),
    memorymongodb.WithSoftDelete(true),
)
if err != nil {
    panic(err)
}
defer mongodbService.Close()
```

或者通过 `storage/mongodb` 注册表注册命名实例，再按名称引用：

```go
import (
    memorymongodb "trpc.group/trpc-go/trpc-agent-go/memory/mongodb"
    storagemongodb "trpc.group/trpc-go/trpc-agent-go/storage/mongodb"
)

storagemongodb.RegisterMongoDBInstance("memory-mongo",
    storagemongodb.WithClientBuilderDSN("mongodb://localhost:27017"),
)

mongodbService, err := memorymongodb.NewService(
    memorymongodb.WithMongoInstance("memory-mongo"),
)
if err != nil {
    panic(err)
}
defer mongodbService.Close()
```

同时设置时，`WithMongoClientURI` 的优先级高于 `WithMongoInstance`。二者必须设置其一，
否则 `NewService` 返回错误。

## 配置选项

| Option | 说明 | 默认值 |
| ------ | ---- | ------ |
| `WithMongoClientURI(uri)` | MongoDB 连接 URI，例如 `mongodb://user:pass@host1:27017,host2:27017/?replicaSet=rs0`。优先级最高 | （必填，URI/实例二选一） |
| `WithMongoInstance(name)` | 使用经 `storage/mongodb.RegisterMongoDBInstance` 预注册的实例 | （必填，URI/实例二选一） |
| `WithDatabase(name)` | 数据库名 | `"trpc-agent-go-mongo-memory"` |
| `WithCollectionName(name)` | 集合名。必须以字母或下划线开头，只含字母数字和下划线，最长 64 字符；非法时 panic | `"memories"` |
| `WithSkipDBInit(skip)` | 跳过索引创建。适用于无 `createIndex` 权限、索引由迁移工具管理或生产环境预建索引的场景 | `false` |
| `WithSoftDelete(enabled)` | 启用软删除：删除操作设置 `deleted_at`，查询自动过滤 tombstone | `false`（硬删除） |
| `WithMemoryLimit(limit)` | 每用户记忆上限。`0` 表示不检查上限 | `1000` |
| `WithMinSearchScore(score)` | 关键词搜索最低分数，低于该分数的结果被过滤。负数会被忽略 | `0.3` |
| `WithMaxResults(n)` | 关键词搜索最大结果数。`0` 表示不截断；负数会被忽略 | `10` |
| `WithCustomTool(toolName, creator)` | 注册自定义记忆工具，默认启用 | — |
| `WithToolEnabled(toolName, enabled)` | 启用/禁用记忆工具；无论 option 顺序如何，均优先于 Auto 模式默认值 | — |
| `WithAutoMemoryExposedTools(names...)` | 在 Auto 模式下通过 `Tools()` 暴露已启用的工具 | — |
| `WithToolExposed(toolName, exposed)` | 控制已启用工具是否通过 `Tools()` 暴露 | — |
| `WithExtractor(e)` | 配置记忆提取器，启用 Auto 模式 | — |
| `WithAsyncMemoryNum(n)` | 异步记忆 worker 数量。小于 `1` 时重置为默认值 | `1` |
| `WithMemoryQueueSize(size)` | 记忆任务队列大小。小于 `1` 时重置为默认值 | `10` |
| `WithMemoryJobTimeout(d)` | 每个记忆提取任务的超时时间 | `30s` |
| `WithDisableAutoMemoryOnExternalContext(disable)` | 对消费了框架外部上下文的会话跳过自动提取 | `false` |
| `WithExtraOptions(...)` | 传递给 MongoDB client builder 的额外选项 | — |

## 存储结构与索引

每条记忆存储为一个文档：

```text
{
    app_name:    string,           // 应用名
    user_id:     string,           // 用户 ID
    memory_id:   string,           // 规范记忆 ID（SHA256 十六进制）
    memory_data: string,           // json.Marshal(memory.Entry)，JSON 字符串
    created_at:  time,
    updated_at:  time,
    deleted_at:  time | 不存在      // 仅软删除 tombstone 上存在
}
```

`memory_data` 是 `memory.Entry` 的 JSON 字符串，与 `memory.Memory` 的 JSON tag 线协议
完全一致（与 MySQL 后端的 `memory_data` 列相同）。标量字段用于索引和过滤。

服务构造时会自动创建两个索引（30 秒超时），除非设置 `WithSkipDBInit(true)`：

- **唯一索引** `(app_name, user_id, memory_id)`，带 partial filter
  `deleted_at: null`，因此软删除的 tombstone 不占用唯一槽位，可以原地复活。
- **查询索引** `(app_name, user_id)`，用于按用户读取、搜索和清空。

## 软删除

默认关闭软删除（硬删除）。设置 `WithSoftDelete(true)` 后，`DeleteMemory` 和
`ClearMemories` 会设置 `deleted_at` 而不是移除文档，所有查询按 `deleted_at: null`
过滤。对相同内容的幂等 `AddMemory` 会通过 `$unset deleted_at` 复活匹配的
tombstone，并保留其原始 `created_at`；更新轮转到已 tombstone 的规范 ID 时同样
会复活目标记录。

## 搜索行为

`SearchMemories` 加载用户的活跃记忆，在应用层用归一化的 BM25 风格关键词分数
排序——与 `mysql` 和 `postgres` 后端行为一致。低于 `WithMinSearchScore` 的结果
被过滤，并按 `WithMaxResults` 截断。空查询返回空结果。

**不支持向量搜索。** 如需语义相似度检索，请使用 pgvector、mysqlvec、sqlitevec 或
ChromaDB 后端，或等待后续与 MongoDB Atlas `vectorSearch` 的集成。

## 集成测试

单元测试不依赖外部服务。针对真实 MongoDB 部署的集成测试由 `integration` build
tag 和 `MONGODB_INTEGRATION_URI` 环境变量门控，未设置该变量时自动跳过：

```bash
export MONGODB_INTEGRATION_URI="mongodb://localhost:27017"
go test -tags=integration ./memory/mongodb/...
```

每个集成测试使用独立的数据库，并在测试结束时 drop。后端不依赖多文档事务，
因此单机（非副本集）部署即可运行。

**资源清理**：调用 `Close()` 停止异步 worker 并断开 MongoDB 客户端连接。`Close`
是幂等的。
