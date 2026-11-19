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
| `WithTrackEventTTL(ttl time.Duration)` | `time.Duration` | 继承 SessionTTL | Track event TTL。非正数表示 Track event 不过期 |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 应用状态 TTL |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 用户状态 TTL |
| `WithCleanupInterval(interval time.Duration)` | `time.Duration` | `0`（自动确定） | 事件和 Track 清理间隔，默认 5 分钟（如果配置了会话或 Track event TTL） |
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

Session events 和 track events 不使用 TTL 索引。默认情况下，它们由服务按会话维度整组清理，避免仍然活跃的会话历史被局部删除。配置 `WithTrackEventTTL` 后，track events 使用该 TTL 独立清理，并通过 `updated_at` cleanup 索引支撑清理扫描。

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

## 过期清理查询开销

清理先找出包含 `updated_at` 不晚于 TTL 截止时间的未删除记录的会话，再利用
原有会话前缀 cleanup 索引，以最多 8 个并发查询检查是否存在更新的未删除记录。
只要存在新记录，整个会话的历史都会保留，包括旧记录。Track 仍按会话整组判断，覆盖该会话全部 track。

初始化为两个事件集合新增非唯一 partial `cleanup_candidates` 索引。历史文档、
旧 cleanup 索引、TTL 配置和删除模式不变。外部管理索引时，可在部署前执行
以下命令，并按实际前缀修改集合和索引名称：

```javascript
for (const name of ["session_events", "session_tracks"]) {
  db.getCollection(name).createIndex(
    {updated_at: 1, app_name: 1, user_id: 1, session_id: 1},
    {name: "idx_" + name + "_cleanup_candidates",
     partialFilterExpression: {deleted_at: null}}
  )
}
```

该方案避免为了寻找候选而扫描大量近期历史，代价是新增索引，以及每个候选会话
一次短查询。历史大部分已旧时，候选扫描仍可能较大，应同时观察查询次数、耗时
和扫描量。缺少索引不影响正确性，旧版本也可以继续使用同一份数据。
大集合建议提前建索引，避免启动时集中产生建索引负载。

候选按每批最多 500 个处理，每批完成后即提交其中已过期的组，不需要凑满
500 个过期组；后续批次失败不会丢失前面批次已完成的删除。事件与 Track
清理依次执行，各自拥有 30 秒预算（合计最多 60 秒），避免事件清理超时后
Track 没有剩余预算。更短的父 context 截止时间或服务关闭仍会取消两者，
清理返回前会等待所有查询工作协程退出。

清理仍沿用先检查、再删除的并发模型：候选检查与后续删除没有和并发追加构成
原子操作。本优化不增加跨服务实例协调，也不改变清理间隔。

## 事件排序索引升级

新服务初始化时新增 `idx_session_events_ordered_lookup` 非唯一 partial 索引，
字段为 `(app_name, user_id, session_id, created_at, _id)`，条件为
`{deleted_at: null}`。集合前缀同样应用于索引名称。该索引支持正序事件读取及
倒序分页，避免额外排序。旧 lookup 索引保留，以支持滚动升级与回滚；
历史文档及旧版本写入无需转换。

初始化建索引会增加启动时间和数据库负载。大集合建议在部署前完成索引创建；
使用 `WithSkipDBInit(true)` 时请手工管理索引（按实际集合前缀调整名称）：

```javascript
db.session_events.createIndex(
  {app_name: 1, user_id: 1, session_id: 1, created_at: 1, _id: 1},
  {name: "idx_session_events_ordered_lookup",
   partialFilterExpression: {deleted_at: null}}
)
```

未创建该索引不影响读取正确性，但可能需要阻塞排序。同时保留两个索引会增加
存储和写入开销。仅增加索引不会限制上下文窗口实际加载的事件数量。

## 上下文窗口读取开销

单个会话设置正数事件上限时，MongoDB 按倒序分批读取最新事件，获得窗口及
所需用户消息后关闭游标，返回顺序和过滤规则保持不变。窗口内没有用户消息时，
仍需向前查找最近的用户消息；整个历史没有用户消息时仍会扫描全部历史。
空 payload 不占事件名额。

时间过滤使用事件 payload 的时间戳，它可能与文档 `created_at` 不同。
候选窗口最早一条事件满足时间条件时，可以直接使用该窗口；否则回退到原有
完整历史过滤，以兼容时间戳乱序时从首个匹配事件开始截取的行为。无限制读取
和多会话读取仍使用原有路径，分页行为不变。排序索引用于避免阻塞排序；
读取不强制指定 hint，因此继续支持外部管理索引的部署。

可选的真实模型测试会验证三轮对话、窗口截取及重建服务后的历史重载。
该测试只发送合成提示词，默认跳过，需显式启用：

```bash
# 在环境中配置 OPENAI_API_KEY、OPENAI_BASE_URL 和 MODEL_NAME。
cd test
MONGODB_LIVE_MODEL_TEST=1 MONGO_TEST_URI='mongodb://localhost:27017/?replicaSet=rs0' \
  go test -run '^TestMongoDBHistoryLiveModel$' -count=1 -v .
```
