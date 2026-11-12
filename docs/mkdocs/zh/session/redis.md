# Redis 存储

Redis 存储适用于生产环境和分布式应用，提供高性能和自动过期能力。Redis Session Service 内部维护两套存储引擎：**HashIdx**（新，默认）和 **ZSet**（旧），通过兼容模式（CompatMode）实现平滑迁移。

## 功能特性

- 基于 Redis 的会话、事件、状态持久化存储
- 支持 Redis Standalone / Sentinel / Cluster 部署模式
- Session、TrackEvent、AppState、UserState 独立 TTL 控制
- 异步持久化（可选），降低写入延迟
- OpenTelemetry 链路追踪（可选）
- 会话摘要（Summary）异步生成
- AppendEvent / GetSession Hook 扩展点

## Redis 兼容性

Redis Session Service 使用的命令兼容 Redis OSS 4.0 及以上版本。Redis 4.0 是文档声明的兼容下限，并非生产环境推荐版本；生产环境应使用 Redis 厂商仍在维护的版本。

当前服务依赖以下数据访问命令：

| 能力 | 命令 |
| --- | --- |
| Key 与字符串 | `DEL`、`EXISTS`、`EXPIRE`、`GET`、`PERSIST`、`PEXPIRE`、`PTTL`、`SCAN`、`SET`、`SETNX` |
| Hash | `HDEL`、`HEXISTS`、`HGET`、`HGETALL`、`HINCRBY`、`HMGET`、`HSCAN`、`HSET` |
| Set | `SADD`、`SMEMBERS` |
| Sorted Set | `ZADD`、`ZRANGE`、`ZRANGEBYSCORE`、`ZRANK`、`ZREM`、`ZREVRANGE`、`ZREVRANGEBYSCORE` |
| Lua 与事务 | `EVAL`、`EVALSHA`、`WATCH`、`UNWATCH`、`MULTI`、`EXEC` |

这里只要求当前服务实际使用的命令形式，不依赖后续 Redis 版本新增的可选参数。Lua 脚本使用 Redis 的 Lua 5.1 环境和 `cjson`。

Redis Standalone、Sentinel 和 Cluster 均可兼容，但 client builder 必须返回与部署模式对应的 `go-redis` 客户端。`WithRedisClientURL` 使用的默认 builder 只会根据单个地址创建单节点客户端；使用 Sentinel 或 Cluster 时，需要通过 `storage/redis.SetClientBuilder` 配置自定义 builder。

在 Cluster 模式下，同一个多 Key Lua 脚本或事务涉及的所有 Key 必须位于同一 hash slot。内置 Key 格式通过 `{userID}` 或 `{appName}` hash tag 满足该要求；按用户执行的 `SCAN` pattern 也包含相同的固定 hash tag，Cluster 客户端可以将其路由到对应 slot 所在节点。

对于 Redis 兼容代理或其他兼容实现，需要单独验证 `SCAN`/`HSCAN`、Lua 5.1 与 `cjson`、事务及脚本缓存行为。若后端的 `EVALSHA` 缓存不可靠，可使用 `WithDisableScriptCache(true)` 仅通过 `EVAL` 执行脚本。根据连接 URL 的配置，内置 `go-redis` 客户端还可能发送 `AUTH`、`SELECT`、`CLIENT SETNAME`，并探测 `HELLO`、`CLIENT SETINFO` 等较新的命令。Redis OSS 4.0 会为不支持的探测命令返回普通错误，客户端随后回退或继续初始化，但遇到未知命令时直接断开连接的代理需要另行验证。

Redis 4.x 和 5.x 不支持 ACL 用户名。这些版本启用鉴权时应仅配置密码，例如 `redis://:password@127.0.0.1:6379/0`；用户名加密码的鉴权方式要求 Redis 6.0 及以上版本。

## 配置选项

**连接配置：**

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithRedisClientURL(url string)` | `string` | - | 通过 URL 创建 Redis 客户端，格式：`redis://[username:password@]host:port[/database]` |
| `WithRedisInstance(instanceName string)` | `string` | - | 使用预配置的 Redis 实例（优先级低于 URL） |
| `WithExtraOptions(extraOptions ...any)` | `[]any` | `nil` | 为 Redis 客户端设置额外选项，会传递给底层的 client builder |

**会话配置：**

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | 每个会话存储的最大事件数量 |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 会话状态和事件的 TTL，负值等同于 0 |
| `WithTrackEventTTL(ttl time.Duration)` | `time.Duration` | 继承 SessionTTL | Track event TTL。非正数表示 Track event 不过期 |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 应用级状态的 TTL |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0`（不过期） | 用户级状态的 TTL |
| `WithKeyPrefix(prefix string)` | `string` | `""` | Redis key 前缀，所有 key 将以 `prefix:` 开头。适用于多应用共享同一 Redis 实例的场景 |
| `WithCompatMode(mode CompatMode)` | `CompatMode` | `CompatModeLegacy` | 存储兼容模式。可选值：`CompatModeNone`、`CompatModeLegacy`、`CompatModeTransition`。详见[存储兼容模式](#存储兼容模式compatmode) |
| `WithEnableUserSessionIndex(enable bool)` | `bool` | `false` | 启用 HashIdx 的用户级 Session 索引。详见[用户级 Session 索引](#用户级-session-索引) |
| `WithDisableScriptCache(disable bool)` | `bool` | `false` | 默认保持 `EVALSHA`-first；启用后仅通过 `EVAL` 执行 Lua 脚本，适用于脚本缓存不可靠的后端，但每次调用都会发送完整脚本，增加带宽开销 |

**异步持久化配置：**

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithEnableAsyncPersist(enable bool)` | `bool` | `false` | 启用异步持久化。启用后 `AppendEvent` 和 `AppendTrackEvent` 会将事件写入内部 channel，由后台 worker 异步写入 Redis，降低请求延迟 |
| `WithAsyncPersisterNum(num int)` | `int` | `10` | 异步持久化 worker 数量。每个 worker 同时处理 Event 和 TrackEvent 各一个 channel，channel 缓冲区大小为 100 |

**摘要配置：**

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | 注入会话摘要器。未设置时摘要相关操作为空操作 |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | 摘要处理 worker 数量 |
| `WithSummaryQueueSize(size int)` | `int` | `100` | 摘要任务队列大小 |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | 单个摘要任务超时时间 |

**链路追踪配置：**

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `WithEnableTracing(enable bool)` | `bool` | `false` | 启用 OpenTelemetry 链路追踪。启用后 `CreateSession`、`GetSession`、`AppendEvent`、`DeleteSession`、`AppendTrackEvent`、`CreateSessionSummary`、`GetSessionSummaryText` 等操作会自动创建 span |

> **关于 Root Span**
>
> Session 的操作由 Runner 执行，发生在 Agent 的 `Run()` 调用前后。而 Agent 的 root span 是在 `agent.Run()` 内部创建的，Session span 不会自动挂载到 Agent span 下。因此，如果需要在 Langfuse 等可观测平台中看到完整的 Session span 链路，需要在调用 `runner.Run()` 之前手动创建一个 root span：
>
> ```go
> import atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
>
> // Create a root span before runner.Run(), so that session spans
> // (create_session, get_session, append_event, etc.) become children
> // of this root span via context propagation.
> ctx, span := atrace.Tracer.Start(ctx, "my_request")
> defer span.End()
>
> eventChan, err := r.Run(ctx, userID, sessionID, message)
> ```

**Hook 配置：**

| 选项 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
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
// - 连接到本地 Redis 0 号数据库
// - 每个会话最多 1000 个事件
// - 会话 30 分钟无活动后自动过期（Redis TTL）
// - 应用状态 24 小时后过期
// - 用户状态 7 天后过期
// - 利用 Redis 原生 TTL 机制，无需手动清理
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
    redis.WithSummaryJobTimeout(120*time.Second),
)
```

## 异步持久化

启用异步持久化后，`AppendEvent` 和 `AppendTrackEvent` 不再同步写入 Redis，而是将事件投递到内部 channel，由后台 worker 协程消费并写入 Redis。这样可以显著降低请求延迟，适用于对写入延迟敏感的场景。

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithEnableAsyncPersist(true),
    redis.WithAsyncPersisterNum(10), // 10 个 worker 协程
)
```

工作原理：

- 每个 worker 协程持有一个 Event channel 和一个 TrackEvent channel（缓冲区大小 100）。
- `AppendEvent` 根据 `session.Hash % workerNum` 选择 channel，保证同一会话的事件有序写入。
- 如果 channel 已满且 context 被取消，会返回 `context.Canceled` 错误。
- 异步写入超时时间为 2 秒（`defaultAsyncPersistTimeout`）。
- 调用 `Close()` 时会关闭所有 channel 并等待 worker 完成剩余任务。

> **注意**
>
> 异步持久化模式下，如果服务进程异常退出，channel 中尚未消费的事件可能会丢失。请根据业务对数据一致性的要求评估是否启用。

## 用户级 Session 索引

`WithEnableUserSessionIndex(true)` 是一个只对 HashIdx 存储生效的可选能力。它会维护一个按用户维度组织的索引，用来建立 `userID -> 该用户创建的 sessionID` 之间的关联映射。

这个索引当前的主要目的是避免 `ListSessions` 里现有的 SCAN 操作。

该选项更适合新写入的 HashIdx 数据。如果在一个已经存在历史 HashIdx session 的环境里直接开启它，这些旧 session 不会自动出现在 index 中，除非额外做迁移或重建索引。

## 存储兼容模式（CompatMode）

新版本 Redis Session 使用了全新的存储引擎（HashIdx），按用户维度散列到不同的 Redis Cluster slot，消除了旧版本所有数据集中在同一 slot 的热点问题。如果你有旧版本的数据需要迁移，可以通过 `WithCompatMode` 配置兼容模式实现平滑过渡。

> **大多数情况下无需关注兼容模式**
>
> 默认的 `CompatModeLegacy` 模式已经能够自动处理新旧数据的读写兼容，**直接升级即可正常工作**。只有在以下两种情况下才需要关注兼容模式配置：
>
> 1. **大量使用了 UserState**：新旧引擎的 UserState 使用不同的 Redis Key，`CreateSession`/`GetSession` 内部合并 UserState 时仅读取新 key，升级后旧 UserState 数据不会自动带入新 session。如果业务重度依赖 UserState，需要按下文说明选择合适的兼容模式。
> 2. **大规模多节点灰度发布**：新旧版本实例同时运行时，需要使用 `CompatModeTransition` 保证混合部署兼容。

### 新业务（无历史数据）

**直接使用 `CompatModeNone`**，跳过所有兼容逻辑，获得最佳性能：

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithCompatMode(redis.CompatModeNone),
)
```

### 单节点或少量节点升级

如果是单节点部署，或者节点数量较少可以一次性全量升级，**直接升级即可**，使用默认的 `CompatModeLegacy`（无需显式设置）：

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    // CompatModeLegacy 是默认值，可省略
)
```

`CompatModeLegacy` 模式下，新创建的 session 使用新存储引擎，旧数据仍可通过回退读取访问，等旧数据按 TTL 自然过期后即可切换到 `CompatModeNone`。

### 大规模多节点灰度升级

大量节点部署且需要灰度发布时，新旧版本实例会同时运行，需要按以下步骤操作：

**第一步：灰度阶段 — 设置 `CompatModeTransition`**

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithCompatMode(redis.CompatModeTransition),
)
```

Transition 模式下，新实例的读写行为与旧实例完全一致（Session 创建走旧存储，UserState 双写新旧两种 key），保证混合部署下数据完全兼容。

**第二步：全量升级完成 — 切换到 `CompatModeLegacy`**

所有实例均已升级后，去掉 `WithCompatMode` 配置（或显式设为 `CompatModeLegacy`）。此后新 session 使用新存储引擎，旧数据仍可回退读取。

**第三步：旧数据过期 — 切换到 `CompatModeNone`**

等旧数据按 TTL 自然过期后（未设置 TTL 则需手动清理），切换到 `CompatModeNone`，移除兼容层。

### 兼容模式对照表

| 模式 | Session 读 | Session 写 | UserState 读 | UserState 写 | 适用场景 |
| --- | --- | --- | --- | --- | --- |
| `CompatModeNone` | 仅新引擎 | 仅新引擎 | 仅新 key | 仅新 key | **新业务**，或旧数据已全部过期 |
| `CompatModeLegacy`（默认） | 旧引擎优先，回退新引擎 | 仅新引擎 | 新 key 优先，回退旧 key | 仅新 key | **单节点/少量节点**直接升级 |
| `CompatModeTransition` | 旧引擎优先，回退新引擎 | 仅旧引擎 | 新 key 优先，回退旧 key | 双写新旧 key | **大规模灰度**，新旧实例混合部署 |

### UserState 迁移注意事项

新旧存储引擎的 UserState 使用了不同的 Redis Key（旧：`userstate:{appName}:{userID}`，新：`hashidx:userstate:appName:{userID}`）。

- 在 `CompatModeTransition` 模式下，`UpdateUserState` 会同时写入新旧两种 key。建议在灰度阶段通过 `UpdateUserState` 重新写入一次 UserState，将数据同步到新 key。
- `ListUserStates` API 在 Transition 和 Legacy 模式下会先尝试新 key，为空时回退旧 key。但 `CreateSession`/`GetSession` 内部合并 UserState 时仅读取新 key，不经过回退。
- **AppState 不受影响**——`appstate:{appName}` 在两种引擎下格式完全一致，零迁移成本。

## 使用场景

| 场景 | 推荐配置 |
| --- | --- |
| 新业务 | `CompatModeNone` |
| 单节点/少量节点升级 | 默认 `CompatModeLegacy`，直接升级 |
| 大规模灰度升级 | `CompatModeTransition` → `CompatModeLegacy` → `CompatModeNone` |
| 生产环境 | 配置 TTL、启用异步持久化 |
| 分布式部署 | 使用 Redis 集群 |
| 高并发场景 | 增加 AsyncPersisterNum |

## 注意事项

1. **连接配置**：确保 Redis 服务可访问，建议使用连接池
2. **TTL 管理**：Redis 原生支持 TTL，无需额外清理任务
3. **内存管理**：监控 Redis 内存使用，配置合理的 maxmemory
4. **高可用**：生产环境建议使用 Redis Sentinel 或 Cluster
5. **优先级**：`WithRedisClientURL` 优先级高于 `WithRedisInstance`
6. **异步持久化风险**：进程异常退出时 channel 中未消费事件可能丢失，需评估业务容忍度
