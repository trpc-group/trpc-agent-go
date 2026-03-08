# Redis 存储

Redis 存储适用于生产环境和分布式应用，提供高性能和自动过期能力。

## 特点

- ✅ 数据持久化
- ✅ 支持分布式
- ✅ 高性能读写
- ✅ 原生 TTL 支持
- ✅ 支持异步持久化

## 配置选项

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithRedisClientURL(url string)` | `string` | - | 通过 URL 创建 Redis 客户端，格式：`redis://[username:password@]host:port[/database]` |
| `WithRedisInstance(instanceName string)` | `string` | - | 使用预配置的 Redis 实例（优先级低于 URL） |
| `WithExtraOptions(extraOptions ...any)` | `[]any` | `nil` | 为 Redis 客户端设置额外选项 |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | 每个会话存储的最大事件数量 |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 会话状态和事件的 TTL |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 应用级状态的 TTL |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 用户级状态的 TTL |
| `WithEnableAsyncPersist(enable bool)` | `bool` | `false` | 启用异步持久化 |
| `WithAsyncPersisterNum(num int)` | `int` | `10` | 异步持久化 worker 数量 |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | 注入会话摘要器 |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | 摘要处理 worker 数量 |
| `WithSummaryQueueSize(size int)` | `int` | `100` | 摘要任务队列大小 |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | 单个摘要任务超时时间 |
| `WithKeyPrefix(prefix string)` | `string` | `""` | Redis key 前缀，所有 key 将以 `prefix:` 开头 |
| `WithAppendEventHook(hooks ...session.AppendEventHook)` | `[]session.AppendEventHook` | `nil` | 添加事件写入 Hook |
| `WithGetSessionHook(hooks ...session.GetSessionHook)` | `[]session.GetSessionHook` | `nil` | 添加会话读取 Hook |

## 基础配置示例

```go
import "trpc.group/trpc-go/trpc-agent-go/session/redis"

// 通过 URL 创建（推荐）
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://username:password@127.0.0.1:6379/0"),
    redis.WithSessionEventLimit(500),
)

// 生产环境完整配置
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379/0"),
    redis.WithSessionEventLimit(1000),
    redis.WithSessionTTL(30*time.Minute),
    redis.WithAppStateTTL(24*time.Hour),
    redis.WithUserStateTTL(7*24*time.Hour),
)
// 效果：
// - 连接本地 Redis 数据库 0
// - 每个会话最多存储 1000 个事件
// - 会话最后一次写入后 30 分钟过期（Redis TTL）
// - 应用状态 24 小时后过期
// - 用户状态 7 天后过期
// - 使用 Redis 原生 TTL 机制，无需手动清理
```

## 配置复用

如果多个组件需要使用同一 Redis 实例，可以注册后复用：

```go
import (
    redisstorage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
    "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

// 注册 Redis 实例
redisURL := "redis://127.0.0.1:6379"
redisstorage.RegisterRedisInstance("my-redis-instance",
    redisstorage.WithClientBuilderURL(redisURL))

// 在会话服务中使用
sessionService, err := redis.NewService(
    redis.WithRedisInstance("my-redis-instance"),
    redis.WithSessionEventLimit(500),
)
```

## 配合摘要使用

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithSessionEventLimit(1000),
    redis.WithSessionTTL(30*time.Minute),

    // 摘要配置
    redis.WithSummarizer(summarizer),
    redis.WithAsyncSummaryNum(4),
    redis.WithSummaryQueueSize(200),
)
```

## 异步持久化

启用异步持久化可以提高写入性能：

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithEnableAsyncPersist(true),
    redis.WithAsyncPersisterNum(10),
)
```

## 存储结构

Redis 存储使用以下 Key 结构：

```
# 应用状态
appstate:{appName} -> Hash {key: value}

# 用户状态
userstate:{appName}:{userID} -> Hash {key: value}

# 会话数据
sess:{appName}:{userID} -> Hash {sessionID: SessionData(JSON)}

# 事件记录
event:{appName}:{userID}:{sessionID} -> SortedSet {score: timestamp, value: Event(JSON)}

# Track 事件
track:{appName}:{userID}:{sessionID}:{trackName} -> SortedSet {score: timestamp, value: TrackEvent(JSON)}

# 摘要数据（可选）
sesssum:{appName}:{userID} -> Hash {sessionID:filterKey: Summary(JSON)}
```

## 使用场景

| 场景 | 推荐配置 |
| --- | --- |
| 生产环境 | 配置 TTL、启用异步持久化 |
| 分布式部署 | 使用 Redis 集群 |
| 高并发场景 | 增加 AsyncPersisterNum |
| 需要数据持久化 | 配置 Redis 持久化策略 |

## 注意事项

1. **连接配置**：确保 Redis 服务可访问，建议使用连接池
2. **TTL 管理**：Redis 原生支持 TTL，无需额外清理任务
3. **内存管理**：监控 Redis 内存使用，配置合理的 maxmemory
4. **高可用**：生产环境建议使用 Redis Sentinel 或 Cluster
5. **优先级**：`WithRedisClientURL` 优先级高于 `WithRedisInstance`
