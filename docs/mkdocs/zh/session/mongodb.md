# MongoDB 存储

MongoDB 存储适用于偏好文档存储，同时需要持久化会话状态、事件历史、Track 事件和摘要的生产环境。

## 部署要求

MongoDB 会话存储要求部署支持多文档事务，例如副本集或分片集群。Standalone MongoDB 不受支持，因为事件和 Track 持久化需要在一个事务内同时更新会话状态并追加历史记录。

## 特点

- ✅ 数据持久化
- ✅ 支持分布式
- ✅ 支持软删除
- ✅ 支持集合前缀
- ✅ 支持异步持久化
- ✅ 支持 Track Service
- ✅ 支持 Event Window

## 配置选项

### 连接配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithMongoClientURI(uri string)` | `string` | - | MongoDB URI，例如 `mongodb://user:pass@host1:27017,host2:27017/?replicaSet=rs0` |
| `WithMongoInstance(instanceName string)` | `string` | - | 使用预配置的 MongoDB 实例（优先级低于 URI） |
| `WithDatabase(database string)` | `string` | `trpc-agent-go-mongo-session` | MongoDB 数据库名 |
| `WithExtraOptions(extraOptions ...any)` | `[]any` | `nil` | 为自定义客户端构建器设置额外选项 |

**优先级**：`WithMongoClientURI` > `WithMongoInstance`

### 会话配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | context-window 模式下每个会话最大事件数量 |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 会话 TTL |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 应用状态 TTL |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 用户状态 TTL |
| `WithCleanupInterval(interval time.Duration)` | `time.Duration` | `0`（自动确定） | 事件和 Track 清理间隔，默认 5 分钟（如果配置了会话 TTL） |
| `WithSoftDelete(enable bool)` | `bool` | `true` | 启用或禁用软删除 |

### 异步持久化配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithEnableAsyncPersist(enable bool)` | `bool` | `false` | 启用 session event 和 track event 异步持久化 |
| `WithAsyncPersisterNum(num int)` | `int` | `10` | 异步持久化 worker 数量 |

### 摘要配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | 注入会话摘要器 |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | 摘要处理 worker 数量 |
| `WithSummaryQueueSize(size int)` | `int` | `100` | 摘要任务队列大小 |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | 单个摘要任务超时时间 |
| `WithSummaryFilterAllowlist(filterKeys ...string)` | `[]string` | `nil` | 限制分支摘要 filter key |
| `WithCascadeFullSessionSummary(enable bool)` | `bool` | `true` | 允许的分支摘要完成后刷新全会话摘要 |

### 集合配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithCollectionPrefix(prefix string)` | `string` | `""` | 集合名前缀 |
| `WithSkipDBInit(skip bool)` | `bool` | `false` | 跳过自动索引初始化和事务探测 |

### Hook 配置

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithAppendEventHook(hooks ...session.AppendEventHook)` | `[]session.AppendEventHook` | `nil` | 添加事件写入 Hook |
| `WithGetSessionHook(hooks ...session.GetSessionHook)` | `[]session.GetSessionHook` | `nil` | 添加会话读取 Hook |

## 基础配置示例

```go
import "trpc.group/trpc-go/trpc-agent-go/session/mongodb"

sessionService, err := mongodb.NewService(
    mongodb.WithMongoClientURI("mongodb://user:password@localhost:27017/?replicaSet=rs0"),
    mongodb.WithDatabase("trpc_agent_go"),
)
```

## 配置复用

```go
import (
    storagemongodb "trpc.group/trpc-go/trpc-agent-go/storage/mongodb"
    sessionmongodb "trpc.group/trpc-go/trpc-agent-go/session/mongodb"
)

storagemongodb.RegisterMongoDBInstance("my-mongodb-instance",
    storagemongodb.WithClientBuilderDSN("mongodb://user:password@localhost:27017/?replicaSet=rs0"),
)

sessionService, err := sessionmongodb.NewService(
    sessionmongodb.WithMongoInstance("my-mongodb-instance"),
    sessionmongodb.WithDatabase("trpc_agent_go"),
)
```

## 集合前缀

MongoDB 支持集合前缀配置，适用于多应用共享数据库的场景：

```go
sessionService, err := mongodb.NewService(
    mongodb.WithMongoClientURI("mongodb://user:password@localhost:27017/?replicaSet=rs0"),
    mongodb.WithCollectionPrefix("app1_"), // app1_session_states
)
```

## 过期与清理

会话状态、应用状态和用户状态使用 MongoDB `expires_at` TTL 索引。摘要不设置独立 TTL，跟随会话生命周期。

Session events 和 track events 不使用 TTL 索引。它们由服务按会话维度整组清理，避免仍然活跃的会话历史被局部删除。

## 存储结构

MongoDB 使用以下集合：

- `session_states`
- `session_events`
- `session_tracks`
- `session_summaries`
- `app_states`
- `user_states`

## 集成测试

集成测试要求 MongoDB 副本集或分片集群，并通过 `integration` build tag 和 `MONGODB_INTEGRATION_URI` 显式启用：

```bash
MONGODB_INTEGRATION_URI='mongodb://user:pass@host:27017/?replicaSet=rs0' \
  go test -tags=integration -count=1 ./...
```
