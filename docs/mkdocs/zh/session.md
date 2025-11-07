# Session 会话管理

## 概述

tRPC-Agent-Go 框架提供了强大的会话（Session）管理功能，用于维护 Agent 与用户交互过程中的对话历史和上下文信息。会话管理模块支持多种存储后端，包括内存存储和 Redis 存储，为 Agent 应用提供了灵活的状态持久化能力。

### 🎯 核心特性

- **会话持久化**：保存完整的对话历史和上下文
- **多存储后端**：支持内存存储、Redis 存储和 PostgreSQL 存储
- **事件追踪**：完整记录会话中的所有交互事件
- **多级存储**：支持应用级、用户级和会话级数据存储
- **并发安全**：内置读写锁保证并发访问安全
- **自动管理**：在 Runner 中指定 Session Service 后，即可自动处理会话的创建、加载和更新
- **软删除支持**：PostgreSQL 存储支持软删除，数据可恢复

## 核心概念

### 会话层次结构

```
Application (应用)
├── User Sessions (用户会话)
│   ├── Session 1 (会话1)
│   │   ├── Session Data (会话数据)
│   │   └── Events (事件列表)
│   └── Session 2 (会话2)
│       ├── Session Data (会话数据)
│       └── Events (事件列表)
└── App Data (应用数据)
```

### 数据层级

- **App Data（应用数据）**：全局共享数据，如系统配置、特性标志等
- **User Data（用户数据）**：用户级别数据，同一用户的所有会话共享，如用户偏好设置
- **Session Data（会话数据）**：会话级别数据，存储单次对话的上下文和状态

## 使用示例

### 集成 Session Service

使用 `runner.WithSessionService` 可以为 Agent 运行器提供完整的会话管理能力，如果未指定，则默认使用基于内存的会话管理。Runner 会自动处理会话的创建、加载和更新，用户无需额外操作，也不用关心内部细节：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/session/redis"
    "trpc.group/trpc-go/trpc-agent-go/session/postgres"
)

// 选择会话服务类型
var sessionService session.Service

// 方式1：使用内存存储（开发测试）
sessionService = inmemory.NewSessionService()

// 方式2：使用 Redis 存储（生产环境）
sessionService, err = redis.NewService(
    redis.WithRedisClientURL("redis://your-username:yourt-password@127.0.0.1:6379"),
)

// 方式3：使用 PostgreSQL 存储（生产环境，支持复杂查询）
sessionService, err = postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithPort(5432),
    postgres.WithUser("postgres"),
    postgres.WithPassword("your-password"),
    postgres.WithDatabase("trpc_sessions"),
)

// 创建 Runner 并配置会话服务
runner := runner.NewRunner(
    "my-agent",
    llmAgent,
    runner.WithSessionService(sessionService), // 关键配置
)

// 使用 Runner 进行多轮对话
eventChan, err := runner.Run(ctx, userID, sessionID, userMessage)
```

Agent 集成会话管理之后即可自动的会话管理能力，包括

1. **自动会话持久化**：每次 AI 交互都会自动保存到会话中
2. **上下文连续性**：自动加载历史对话上下文，实现真正的多轮对话
3. **状态管理**：维护应用、用户和会话三个层级的状态数据
4. **事件流处理**：自动记录用户输入、AI 响应、工具调用等所有交互事件

### 基本会话操作

如果用户需要手动管理已有的会话，比如查询统计已有的 Session，可以使用 Session Service 提供的 API。

#### 创建和管理会话

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/session"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/event"
)

func main() {
    // 创建内存会话服务
    sessionService := inmemory.NewSessionService()

    // 创建会话
    key := session.Key{
        AppName:   "my-agent",
        UserID:    "user123",
        SessionID: "", // 空字符串会自动生成 UUID
    }

    initialState := session.StateMap{
        "language": []byte("zh-CN"),
        "theme":    []byte("dark"),
    }

    createdSession, err := sessionService.CreateSession(
        context.Background(),
        key,
        initialState,
    )
    if err != nil {
        panic(err)
    }

    fmt.Printf("Created session: %s\n", createdSession.ID)
}
```

#### GetSession - 获取会话

```go
// GetSession 通过会话键获取指定会话
func (s *SessionService) GetSession(
    ctx context.Context,
    key session.Key,
    options ...session.Option,
) (*Session, error)
```

**功能**：根据 AppName、UserID 和 SessionID 检索已存在的会话

**参数**：

- `key`：会话键，必须包含完整的 AppName、UserID 和 SessionID
- `options`：可选参数，如 `session.WithEventNum(10)` 限制返回的事件数量

**返回值**：

- 如果会话不存在返回 `nil, nil`
- 如果会话存在返回完整的会话对象（包含合并的 app、user、session 状态）

**使用示例**：

```go
// 获取完整会话
session, err := sessionService.GetSession(ctx, session.Key{
    AppName:   "my-agent",
    UserID:    "user123",
    SessionID: "session-id-123",
})

// 获取最近 10 个事件的会话
session, err := sessionService.GetSession(ctx, key,
    session.WithEventNum(10))

// 获取指定时间后的事件
session, err := sessionService.GetSession(ctx, key,
    session.WithEventTime(time.Now().Add(-1*time.Hour)))
```

#### DeleteSession - 删除会话

```go
// DeleteSession 删除指定会话
func (s *SessionService) DeleteSession(
    ctx context.Context,
    key session.Key,
    options ...session.Option,
) error
```

**功能**：从存储中移除指定会话，如果用户下没有其他会话则自动清理用户记录

**特点**：

- 删除不存在的会话不会报错
- 自动清理空的用户会话映射
- 线程安全操作

**使用示例**：

```go
// 删除指定会话
err := sessionService.DeleteSession(ctx, session.Key{
    AppName:   "my-agent",
    UserID:    "user123",
    SessionID: "session-id-123",
})
if err != nil {
    log.Printf("Failed to delete session: %v", err)
}
```

#### ListSessions - 列出会话

```go
// 列出用户的所有会话
sessions, err := sessionService.ListSessions(
    context.Background(),
    session.UserKey{
        AppName: "my-agent",
        UserID:  "user123",
    },
)
```

#### 状态管理

```go
// 更新应用状态
appState := session.StateMap{
    "version": []byte("1.0.0"),
    "config":  []byte(`{"feature_flags": {"new_ui": true}}`),
}
err := sessionService.UpdateAppState(context.Background(), "my-agent", appState)

// 更新用户状态
userKey := session.UserKey{
    AppName: "my-agent",
    UserID:  "user123",
}
userState := session.StateMap{
    "preferences": []byte(`{"notifications": true}`),
    "profile":     []byte(`{"name": "Alice"}`),
}
err = sessionService.UpdateUserState(context.Background(), userKey, userState)

// 获取会话（包含合并后的状态）
retrievedSession, err = sessionService.GetSession(
    context.Background(),
    session.Key{
        AppName:   "my-agent",
        UserID:    "user123",
        SessionID: retrievedSession.ID,
    },
)
```

## 存储后端

tRPC-Agent-Go 提供三种会话存储后端，满足不同场景需求：

| 存储类型   | 适用场景           | 优势                           | 劣势                   |
| ---------- | ------------------ | ------------------------------ | ---------------------- |
| 内存存储   | 开发测试、小规模   | 简单快速、无需外部依赖         | 数据不持久、不支持分布式 |
| Redis 存储 | 生产环境、分布式   | 高性能、支持分布式、自动过期   | 需要 Redis 服务        |
| PostgreSQL | 生产环境、复杂查询 | 关系型数据库、支持复杂查询、JSONB | 相对较重、需要数据库   |

### 内存存储

适用于开发环境和小规模应用：

```go
import "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

// 创建内存会话服务
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(200), // 限制每个会话最多保存 200 个事件
)
```

#### 内存存储配置选项

- **`WithSessionEventLimit(limit int)`**：设置每个会话存储的最大事件数量。默认值为 1000，超过限制时淘汰老的事件。
- **`WithSessionTTL(ttl time.Duration)`**：设置会话状态和事件列表的 TTL。默认值为 0（不过期），如果设置为 0，会话将不会自动过期。
- **`WithAppStateTTL(ttl time.Duration)`**：设置应用级状态的 TTL。默认值为 0（不过期），如果未设置，应用状态将不会自动过期。
- **`WithUserStateTTL(ttl time.Duration)`**：设置用户级状态的 TTL。默认值为 0（不过期），如果未设置，用户状态将不会自动过期。
- **`WithCleanupInterval(interval time.Duration)`**：设置过期数据自动清理的间隔。默认值为 0（自动确定），如果设置为 0，将根据 TTL 配置自动确定清理间隔。如果配置了任何 TTL，默认清理间隔为 5 分钟。

**完整配置示例：**

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(500),
    inmemory.WithSessionTTL(30*time.Minute),
    inmemory.WithAppStateTTL(24*time.Hour),
    inmemory.WithUserStateTTL(7*24*time.Hour),
    inmemory.WithCleanupInterval(10*time.Minute),
)

// 配置效果说明：
// - 每个会话最多存储 500 个事件，超出时自动淘汰最老的事件
// - 会话数据在 30 分钟无活动后自动过期
// - 应用级状态在 24 小时后过期
// - 用户级状态在 7 天后过期
// - 每 10 分钟执行一次清理操作，移除过期数据
```

**默认配置示例：**

```go
// 使用默认配置创建内存会话服务
sessionService := inmemory.NewSessionService()

// 默认配置效果说明：
// - 每个会话最多存储 1000 个事件（默认值）
// - 所有数据永不过期（TTL 为 0）
// - 不执行自动清理（CleanupInterval 为 0）
// - 适用于开发环境或短期运行的应用
```

### Redis 存储

适用于生产环境和分布式应用：

```go
import "trpc.group/trpc-go/trpc-agent-go/session/redis"

// 使用 Redis URL 创建
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://your-username:yourt-password@127.0.0.1:6379"),
    redis.WithSessionEventLimit(500),
)

// 或使用预配置的 Redis 实例
sessionService, err := redis.NewService(
    redis.WithInstanceName("my-redis-instance"),
)
```

#### Redis 存储配置选项

- **`WithSessionEventLimit(limit int)`**：设置每个会话存储的最大事件数量。默认值为 1000，超过限制时淘汰老的事件。
- **`WithRedisClientURL(url string)`**：通过 URL 创建 Redis 客户端。格式：`redis://[username:password@]host:port[/database]`。
- **`WithRedisInstance(instanceName string)`**：使用预配置的 Redis 实例。注意：`WithRedisClientURL` 的优先级高于 `WithRedisInstance`。
- **`WithExtraOptions(extraOptions ...interface{})`**：为 Redis 会话服务设置额外选项。此选项主要用于自定义 Redis 客户端构建器，将传递给构建器。
- **`WithSessionTTL(ttl time.Duration)`**：设置会话状态和事件列表的 TTL。默认值为 0（不过期），如果设置为 0，会话将不会过期。
- **`WithAppStateTTL(ttl time.Duration)`**：设置应用级状态的 TTL。默认值为 0（不过期），如果未设置，应用状态将不会过期。
- **`WithUserStateTTL(ttl time.Duration)`**：设置用户级状态的 TTL。默认值为 0（不过期），如果未设置，用户状态将不会过期。

**完整配置示例：**

````go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379/0"),
    redis.WithSessionEventLimit(1000),
    redis.WithSessionTTL(30*time.Minute),
    redis.WithAppStateTTL(24*time.Hour),
    redis.WithUserStateTTL(7*24*time.Hour),
)

// 配置效果说明：
// - 连接到本地 Redis 服务器的 0 号数据库
// - 每个会话最多存储 1000 个事件，超出时自动淘汰最老的事件
// - 会话数据在 30 分钟无活动后自动过期
// - 应用级状态在 24 小时后过期
// - 用户级状态在 7 天后过期
// - 利用 Redis 的 TTL 机制自动清理过期数据，无需手动清理

**默认配置示例：**

```go
// 使用默认配置创建 Redis 会话服务（需要预配置 Redis 实例）
sessionService, err := redis.NewService()

// 默认配置效果说明：
// - 每个会话最多存储 1000 个事件（默认值）
// - 所有数据永不过期（TTL 为 0）
// - 需要通过 storage.RegisterRedisInstance 预先注册 Redis 实例
// - 适用于需要持久化但不需要自动过期的场景
````

#### 配置复用

如果你有多个组件需要用到 redis，可以配置一个 redis 实例，然后在多个组件中复用配置。

```go
    redisURL := fmt.Sprintf("redis://%s", "127.0.0.1:6379")
    storage.RegisterRedisInstance("my-redis-instance", storage.WithClientBuilderURL(redisURL))
    sessionService, err = redis.NewService(redis.WithRedisInstance("my-redis-instance"))
```

#### Redis 存储结构

```
# 应用数据
appdata:{appName} -> Hash {key: value}

# 用户数据
userdata:{appName}:{userID} -> Hash {key: value}

# 会话数据
session:{appName}:{userID} -> Hash {sessionID: SessionData(JSON)}

# 事件记录
events:{appName}:{userID}:{sessionID} -> SortedSet {score: timestamp, value: Event(JSON)}
```

### PostgreSQL 存储

适用于生产环境和需要复杂查询的应用：

```go
import "trpc.group/trpc-go/trpc-agent-go/session/postgres"

// 使用连接参数创建
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithPort(5432),
    postgres.WithUser("postgres"),
    postgres.WithPassword("your-password"),
    postgres.WithDatabase("trpc_sessions"),
    postgres.WithSessionEventLimit(500),
)

// 或使用预配置的 PostgreSQL 实例
sessionService, err := postgres.NewService(
    postgres.WithInstanceName("my-postgres-instance"),
)
```

#### PostgreSQL 存储配置选项

**连接配置：**

- **`WithHost(host string)`**：PostgreSQL 服务器地址。默认值为 `localhost`。
- **`WithPort(port int)`**：PostgreSQL 服务器端口。默认值为 `5432`。
- **`WithUser(user string)`**：数据库用户名。默认值为 `postgres`。
- **`WithPassword(password string)`**：数据库密码。默认值为空字符串。
- **`WithDatabase(database string)`**：数据库名称。默认值为 `postgres`。
- **`WithSSLMode(sslMode string)`**：SSL 模式。默认值为 `disable`。可选值：`disable`、`require`、`verify-ca`、`verify-full`。
- **`WithInstanceName(name string)`**：使用预配置的 PostgreSQL 实例。注意：直接连接参数的优先级高于实例名称。

**会话配置：**

- **`WithSessionEventLimit(limit int)`**：设置每个会话存储的最大事件数量。默认值为 1000，超过限制时淘汰老的事件。
- **`WithSessionTTL(ttl time.Duration)`**：设置会话状态和事件列表的 TTL。默认值为 0（不过期）。配置后自动启用 TTL 清理。
- **`WithAppStateTTL(ttl time.Duration)`**：设置应用级状态的 TTL。默认值为 0（不过期）。配置后自动启用 TTL 清理。
- **`WithUserStateTTL(ttl time.Duration)`**：设置用户级状态的 TTL。默认值为 0（不过期）。配置后自动启用 TTL 清理。
- **`WithCleanupInterval(interval time.Duration)`**：设置 TTL 清理间隔。默认值为 5 分钟（当配置了任何 TTL 时自动启用）。设置为负数可禁用自动清理。
- **`WithSoftDelete(enable bool)`**：启用或禁用软删除。默认值为 `true`（启用）。启用时删除操作标记 `deleted_at`，禁用时物理删除记录。

**异步持久化配置：**

- **`WithAsyncPersisterNum(num int)`**：设置异步持久化 worker 数量。默认值为 2。更多 worker 提高并发写入性能。
- **`WithPersistQueueSize(size int)`**：设置持久化任务队列大小。默认值为 1000。

**摘要配置：**

- **`WithSummarizer(s summary.SessionSummarizer)`**：将摘要器注入到会话服务中。
- **`WithAsyncSummaryNum(num int)`**：设置摘要处理 worker 数量。默认值为 2。
- **`WithSummaryQueueSize(size int)`**：设置摘要任务队列大小。默认值为 100。

**完整配置示例：**

```go
sessionService, err := postgres.NewService(
    // 连接配置
    postgres.WithHost("localhost"),
    postgres.WithPort(5432),
    postgres.WithUser("postgres"),
    postgres.WithPassword("your-password"),
    postgres.WithDatabase("trpc_sessions"),
    postgres.WithSSLMode("require"),

    // 会话配置
    postgres.WithSessionEventLimit(1000),
    postgres.WithSessionTTL(30*time.Minute),
    postgres.WithAppStateTTL(24*time.Hour),
    postgres.WithUserStateTTL(7*24*time.Hour),

    // TTL 清理配置
    postgres.WithCleanupInterval(10*time.Minute),  // 每 10 分钟清理一次过期数据
    postgres.WithSoftDelete(true),                 // 启用软删除（默认）

    // 异步持久化配置
    postgres.WithAsyncPersisterNum(4),
    postgres.WithPersistQueueSize(2000),

    // 摘要配置
    postgres.WithSummarizer(summarizer),
    postgres.WithAsyncSummaryNum(2),
    postgres.WithSummaryQueueSize(100),
)

// 配置效果说明：
// - 连接到本地 PostgreSQL 服务器，使用 SSL 加密
// - 每个会话最多存储 1000 个事件
// - 会话数据在 30 分钟无活动后自动过期
// - 应用级状态在 24 小时后过期
// - 用户级状态在 7 天后过期
// - 每 10 分钟自动清理过期数据（软删除模式）
// - 使用 4 个异步 worker 处理持久化任务
// - 持久化队列大小为 2000
```

**默认配置示例：**

```go
// 使用默认配置创建 PostgreSQL 会话服务
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithPassword("your-password"),
)

// 默认配置效果说明：
// - 连接到 localhost:5432，数据库 postgres，用户 postgres
// - 每个会话最多存储 1000 个事件（默认值）
// - 所有数据永不过期（TTL 为 0）
// - 使用 2 个异步持久化 worker
// - 持久化队列大小为 1000
```

#### 配置复用

如果你有多个组件需要用到 PostgreSQL，可以配置一个 PostgreSQL 实例，然后在多个组件中复用配置：

```go
import "trpc.group/trpc-go/trpc-agent-go/storage"

// 注册 PostgreSQL 实例
storage.RegisterPostgresInstance("my-postgres-instance",
    storage.WithPostgresHost("localhost"),
    storage.WithPostgresPort(5432),
    storage.WithPostgresUser("postgres"),
    storage.WithPostgresPassword("your-password"),
    storage.WithPostgresDatabase("trpc_sessions"),
)

// 在会话服务中使用
sessionService, err := postgres.NewService(
    postgres.WithInstanceName("my-postgres-instance"),
)
```

#### PostgreSQL 存储结构

PostgreSQL 存储使用关系型数据库表结构，所有 JSON 数据使用 JSONB 类型存储，支持高效查询和索引：

**表结构：**

```sql
-- 会话状态表
CREATE TABLE session_states (
    id BIGSERIAL PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    state JSONB,                              -- 会话状态（JSONB 格式）
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP,                     -- TTL 支持
    deleted_at TIMESTAMP                      -- 软删除支持
);

-- 部分唯一索引（只对未删除记录生效）
CREATE UNIQUE INDEX idx_session_states_unique_active
ON session_states(app_name, user_id, session_id)
WHERE deleted_at IS NULL;

-- 会话事件表
CREATE TABLE session_events (
    id BIGSERIAL PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    event JSONB NOT NULL,                     -- 事件数据（JSONB 格式）
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,            -- 更新时间
    expires_at TIMESTAMP,
    deleted_at TIMESTAMP
);

-- 会话摘要表
CREATE TABLE session_summaries (
    id BIGSERIAL PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    filter_key VARCHAR(255) NOT NULL,
    summary JSONB NOT NULL,                   -- 摘要数据（JSONB 格式）
    updated_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP,
    deleted_at TIMESTAMP,
    UNIQUE(app_name, user_id, session_id, filter_key)
);

-- 应用状态表
CREATE TABLE app_states (
    id BIGSERIAL PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    key VARCHAR(255) NOT NULL,
    value BYTEA NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP,
    deleted_at TIMESTAMP,
    UNIQUE(app_name, key)
);

-- 用户状态表
CREATE TABLE user_states (
    id BIGSERIAL PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    key VARCHAR(255) NOT NULL,
    value BYTEA NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP,
    deleted_at TIMESTAMP,
    UNIQUE(app_name, user_id, key)
);
```

#### Schema 与表前缀支持

PostgreSQL 存储支持 schema 和表前缀配置，适用于多租户和多环境场景：

**Schema 支持：**

```go
// 使用自定义 schema（表将创建在指定 schema 下）
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithDatabase("mydb"),
    postgres.WithSchema("my_schema"),  // 表名：my_schema.session_states
)

// 独立初始化数据库到指定 schema
err := postgres.InitDB(
    context.Background(),
    postgres.WithInitDBHost("localhost"),
    postgres.WithInitDBDatabase("mydb"),
    postgres.WithInitDBSchema("my_schema"),
)
```

**表前缀支持：**

```go
// 使用表前缀（适用于多应用共享数据库）
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithTablePrefix("app1_"),  // 表名：app1_session_states
)

// 结合 schema 和表前缀使用
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSchema("tenant_a"),
    postgres.WithTablePrefix("app1_"),  // 表名：tenant_a.app1_session_states
)
```

**表命名规则：**

| Schema | Prefix | 最终表名 |
|--------|--------|---------|
| （无） | （无） | `session_states` |
| （无） | `app1_` | `app1_session_states` |
| `my_schema` | （无） | `my_schema.session_states` |
| `my_schema` | `app1_` | `my_schema.app1_session_states` |

**使用场景：**

1. **多租户隔离**：不同租户使用不同 schema
2. **环境隔离**：开发、测试、生产环境使用不同 schema
3. **多应用共享**：多个应用使用不同表前缀避免冲突
4. **权限控制**：通过 schema 级别的权限管理访问控制

**注意事项：**

- Schema 必须在使用前创建：`CREATE SCHEMA IF NOT EXISTS my_schema;`
- Schema 和表前缀仅允许字母、数字和下划线，防止 SQL 注入
- 使用 `WithSkipDBInit()` 可跳过自动建表，适用于无 DDL 权限的场景

#### 软删除与 TTL 清理

PostgreSQL 存储支持软删除和自动 TTL 清理功能：

**软删除配置：**

```go
// 默认启用软删除
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSoftDelete(true),  // 默认值
)

// 禁用软删除（使用硬删除）
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSoftDelete(false),
)
```

**删除行为：**

| 配置 | 删除操作 | 查询行为 | 数据恢复 |
|------|---------|---------|---------|
| `softDelete=true` | `UPDATE SET deleted_at = NOW()` | 过滤 `deleted_at IS NULL` | 可恢复 |
| `softDelete=false` | `DELETE FROM ...` | 过滤 `deleted_at IS NULL` | 不可恢复 |

**TTL 自动清理：**

```go
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSessionTTL(30*time.Minute),      // 会话 30 分钟后过期
    postgres.WithAppStateTTL(24*time.Hour),       // 应用状态 24 小时后过期
    postgres.WithUserStateTTL(7*24*time.Hour),    // 用户状态 7 天后过期
    postgres.WithCleanupInterval(10*time.Minute), // 每 10 分钟清理一次（默认 5 分钟）
    postgres.WithSoftDelete(true),                // 软删除模式（默认）
)

// 清理行为：
// - softDelete=true：过期数据被标记为 deleted_at = NOW()
// - softDelete=false：过期数据被物理删除
// - 查询时始终过滤 deleted_at IS NULL
```


## 会话摘要

### 概述

随着对话的持续增长，维护完整的事件历史可能会占用大量内存，并可能超出 LLM 的上下文窗口限制。会话摘要功能使用 LLM 自动将历史对话压缩为简洁的摘要，在保留重要上下文的同时显著降低内存占用和 token 消耗。

**核心特性：**

- **自动触发**：根据事件数量、token 数量或时间阈值自动生成摘要
- **增量处理**：只处理自上次摘要以来的新事件，避免重复计算
- **LLM 驱动**：使用任何配置的 LLM 模型生成高质量、上下文感知的摘要
- **非破坏性**：原始事件完整保留，摘要单独存储
- **异步处理**：后台异步执行，不阻塞对话流程
- **灵活配置**：支持自定义触发条件、提示词和字数限制

### 基础配置

#### 步骤 1：创建摘要器

使用 LLM 模型创建摘要器并配置触发条件：

```go
import (
    "time"

    "trpc.group/trpc-go/trpc-agent-go/session/summary"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// 创建用于摘要的 LLM 模型
summaryModel, err := openai.NewModel(
    openai.WithAPIKey("your-api-key"),
    openai.WithModelName("gpt-4"),
)
if err != nil {
    panic(err)
}

// 创建摘要器并配置触发条件
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithChecksAny(                     // 任一条件满足即触发
        summary.CheckEventThreshold(20),       // 20 个事件后触发
        summary.CheckTokenThreshold(4000),     // 4000 个 token 后触发
        summary.CheckTimeThreshold(5*time.Minute), // 5 分钟无活动后触发
    ),
    summary.WithMaxSummaryWords(200),          // 限制摘要在 200 字以内
)
```

#### 步骤 2：配置会话服务

将摘要器集成到会话服务（内存或 Redis）：

```go
import (
    "time"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

// 内存存储（开发/测试）
sessionService := inmemory.NewSessionService(
    inmemory.WithSummarizer(summarizer),
    inmemory.WithAsyncSummaryNum(2),                // 2 个异步 worker
    inmemory.WithSummaryQueueSize(100),             // 队列大小 100
    inmemory.WithSummaryJobTimeout(30*time.Second), // 单个任务超时 30 秒
)

// Redis 存储（生产环境）
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithSummarizer(summarizer),
    redis.WithAsyncSummaryNum(4),           // 4 个异步 worker
    redis.WithSummaryQueueSize(200),        // 队列大小 200
)
```

#### 步骤 3：配置 Agent 和 Runner

创建 Agent 并配置摘要注入行为：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// 创建 Agent（配置摘要注入行为）
llmAgent := llmagent.New(
    "my-agent",
    llmagent.WithModel(summaryModel),
    llmagent.WithAddSessionSummary(true),   // 启用摘要注入
    llmagent.WithMaxHistoryRuns(10),        // 配合使用（见下方说明）
)

// 创建 Runner
r := runner.NewRunner(
    "my-agent",
    llmAgent,
    runner.WithSessionService(sessionService),
)

// 运行对话 - 摘要将自动管理
eventChan, err := r.Run(ctx, userID, sessionID, userMessage)
```

完成以上配置后，摘要功能即可自动运行。

### 摘要触发机制

#### 自动触发（推荐）

**Runner 自动触发：** 在每次对话完成后，Runner 会自动检查触发条件，满足条件时在后台异步生成摘要，无需手动干预。

**触发时机：**

- 事件数量达到阈值（`WithEventThreshold`）
- Token 数量达到阈值（`WithTokenThreshold`）
- 距上次事件超过指定时间（`WithTimeThreshold`）
- 满足自定义组合条件（`WithChecksAny` / `WithChecksAll`）

#### 手动触发

某些场景下，你可能需要手动触发摘要：

```go
// 异步摘要（推荐）- 后台处理，不阻塞
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents, // 对完整会话生成摘要
    false,                               // force=false，遵守触发条件
)

// 同步摘要 - 立即处理，会阻塞当前操作
err := sessionService.CreateSessionSummary(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    false, // force=false，遵守触发条件
)

// 异步强制摘要 - 忽略触发条件，强制生成
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    true, // force=true，绕过所有触发条件检查
)

// 同步强制摘要 - 立即强制生成
err := sessionService.CreateSessionSummary(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    true, // force=true，绕过所有触发条件检查
)
```

**API 说明：**

- **`EnqueueSummaryJob`**：异步摘要（推荐）

  - 后台处理，不阻塞当前操作
  - 失败时自动回退到同步处理
  - 适合生产环境

- **`CreateSessionSummary`**：同步摘要
  - 立即处理，会阻塞当前操作
  - 直接返回处理结果
  - 适合调试或需要立即获取结果的场景

**参数说明：**

- **filterKey**：`session.SummaryFilterKeyAllContents` 表示对完整会话生成摘要
- **force 参数**：
  - `false`：遵守配置的触发条件（事件数、token 数、时间阈值等），只有满足条件才生成摘要
  - `true`：强制生成摘要，完全忽略所有触发条件检查，无论会话状态如何都会执行

**使用场景：**

| 场景         | API                    | force   | 说明                         |
| ------------ | ---------------------- | ------- | ---------------------------- |
| 正常自动摘要 | 由 Runner 自动调用     | `false` | 满足触发条件时自动生成       |
| 会话结束     | `EnqueueSummaryJob`    | `true`  | 强制生成最终完整摘要         |
| 用户请求查看 | `CreateSessionSummary` | `true`  | 立即生成并返回               |
| 定时批量处理 | `EnqueueSummaryJob`    | `false` | 批量检查并处理符合条件的会话 |
| 调试测试     | `CreateSessionSummary` | `true`  | 立即执行，方便验证           |

### 上下文注入机制

框架提供两种模式来管理发送给 LLM 的对话上下文：

#### 模式 1：启用摘要注入（推荐）

```go
llmagent.WithAddSessionSummary(true)
```

**工作方式：**

- 摘要作为系统消息自动前置到 LLM 输入
- 包含摘要时间点之后的**所有增量事件**（不截断）
- 保证完整上下文：浓缩历史 + 完整新对话
- **`WithMaxHistoryRuns` 参数被忽略**

**上下文结构：**

```
┌─────────────────────────────────────┐
│ 系统提示词                            │
├─────────────────────────────────────┤
│ 会话摘要（system message）            │ ← 历史对话的浓缩版本
├─────────────────────────────────────┤
│ 事件 1（摘要时间点之后）                │ ┐
│ 事件 2                               │ │
│ 事件 3                               │ │ 摘要后的所有新对话
│ ...                                 │ │ （完整保留，不截断）
│ 事件 N（当前消息）                     │ ┘
└─────────────────────────────────────┘
```

**适用场景：** 长期运行的会话，需要保持完整历史上下文同时控制 token 消耗。

#### 模式 2：不使用摘要

```go
llmagent.WithAddSessionSummary(false)
llmagent.WithMaxHistoryRuns(10)  // 限制历史轮次
```

**工作方式：**

- 不添加摘要消息
- 只包含最近 `MaxHistoryRuns` 轮对话
- `MaxHistoryRuns=0` 时不限制，包含所有历史

**上下文结构：**

```
┌─────────────────────────────────────┐
│ 系统提示词                            │
├─────────────────────────────────────┤
│ 事件 N-k+1                           │ ┐
│ 事件 N-k+2                           │ │ 最近 k 轮对话
│ ...                                 │ │ (MaxHistoryRuns=k)
│ 事件 N（当前消息）                     │ ┘
└─────────────────────────────────────┘
```

**适用场景：** 短会话、测试环境，或需要精确控制上下文窗口大小。

#### 模式选择建议

| 场景                   | 推荐配置                                         | 说明                       |
| ---------------------- | ------------------------------------------------ | -------------------------- |
| 长期会话（客服、助手） | `AddSessionSummary=true`                         | 保持完整上下文，优化 token |
| 短期会话（单次咨询）   | `AddSessionSummary=false`<br>`MaxHistoryRuns=10` | 简单直接，无需摘要开销     |
| 调试测试               | `AddSessionSummary=false`<br>`MaxHistoryRuns=5`  | 快速验证，减少干扰         |
| 高并发场景             | `AddSessionSummary=true`<br>增加 worker 数量     | 异步处理，不影响响应速度   |

### 高级配置

#### 摘要器选项

使用以下选项配置摘要器行为：

**触发条件：**

- **`WithEventThreshold(eventCount int)`**：当事件数量超过阈值时触发摘要。示例：`WithEventThreshold(20)` 在 20 个事件后触发。
- **`WithTokenThreshold(tokenCount int)`**：当总 token 数量超过阈值时触发摘要。示例：`WithTokenThreshold(4000)` 在 4000 个 token 后触发。
- **`WithTimeThreshold(interval time.Duration)`**：当自上次事件后经过的时间超过间隔时触发摘要。示例：`WithTimeThreshold(5*time.Minute)` 在 5 分钟无活动后触发。

**组合条件：**

- **`WithChecksAll(checks ...Checker)`**：要求所有条件都满足（AND 逻辑）。使用 `Check*` 函数（不是 `With*`）。示例：
  ```go
  summary.WithChecksAll(
      summary.CheckEventThreshold(10),
      summary.CheckTokenThreshold(2000),
  )
  ```
- **`WithChecksAny(checks ...Checker)`**：任何条件满足即触发（OR 逻辑）。使用 `Check*` 函数（不是 `With*`）。示例：
  ```go
  summary.WithChecksAny(
      summary.CheckEventThreshold(50),
      summary.CheckTimeThreshold(10*time.Minute),
  )
  ```

**注意：**在 `WithChecksAll` 和 `WithChecksAny` 中使用 `Check*` 函数（如 `CheckEventThreshold`）。将 `With*` 函数（如 `WithEventThreshold`）作为 `NewSummarizer` 的直接选项使用。`Check*` 函数创建检查器实例，而 `With*` 函数是选项设置器。

**摘要生成：**

- **`WithMaxSummaryWords(maxWords int)`**：限制摘要的最大字数。该限制会包含在提示词中以指导模型生成。示例：`WithMaxSummaryWords(150)` 请求在 150 字以内的摘要。
- **`WithPrompt(prompt string)`**：提供自定义摘要提示词。提示词必须包含占位符 `{conversation_text}`，它会被对话内容替换。可选包含 `{max_summary_words}` 用于字数限制指令。

**自定义提示词示例：**

```go
customPrompt := `分析以下对话并提供简洁的摘要，重点关注关键决策、行动项和重要上下文。
请控制在 {max_summary_words} 字以内。

<conversation>
{conversation_text}
</conversation>

摘要：`

summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithPrompt(customPrompt),
    summary.WithMaxSummaryWords(100),
    summary.WithEventThreshold(15),
)
```

#### 会话服务选项

在会话服务中配置异步摘要处理：

- **`WithSummarizer(s summary.SessionSummarizer)`**：将摘要器注入到会话服务中。
- **`WithAsyncSummaryNum(num int)`**：设置用于摘要处理的异步 worker goroutine 数量。默认为 2。更多 worker 允许更高并发但消耗更多资源。
- **`WithSummaryQueueSize(size int)`**：设置摘要任务队列的大小。默认为 100。更大的队列允许更多待处理任务但消耗更多内存。
- **`WithSummaryJobTimeout(timeout time.Duration)`** _（仅内存模式）_：设置处理单个摘要任务的超时时间。默认为 30 秒。

### 手动触发摘要

可以使用会话服务 API 手动触发摘要：

```go
// 同步摘要
err := sessionService.CreateSessionSummary(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents, // 完整会话摘要。
    false,                                // force=false，遵守触发条件。
)

// 异步摘要（推荐）
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    false, // force=false。
)

// 强制摘要，不考虑触发条件
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    true, // force=true，绕过触发条件。
)
```

### 获取摘要

从会话中获取最新的摘要文本：

```go
summaryText, found := sessionService.GetSessionSummaryText(ctx, sess)
if found {
    fmt.Printf("摘要：%s\n", summaryText)
}
```

### 工作原理

1. **增量处理**：摘要器跟踪每个会话的上次摘要时间。在后续运行中，它只处理上次摘要后发生的事件。

2. **增量摘要**：新事件与先前的摘要（作为系统事件前置）组合，生成一个既包含旧上下文又包含新信息的更新摘要。

3. **触发条件评估**：在生成摘要之前，摘要器会评估配置的触发条件（事件计数、token 计数、时间阈值）。如果条件未满足且 `force=false`，则跳过摘要。

4. **异步 Worker**：摘要任务使用基于哈希的分发策略分配到多个 worker goroutine。这确保同一会话的任务按顺序处理，而不同会话可以并行处理。

5. **回退机制**：如果异步入队失败（队列已满、上下文取消或 worker 未初始化），系统会自动回退到同步处理。

### 最佳实践

1. **选择合适的阈值**：根据 LLM 的上下文窗口和对话模式设置事件/token 阈值。对于 GPT-4（8K 上下文），考虑使用 `WithTokenThreshold(4000)` 为响应留出空间。

2. **使用异步处理**：在生产环境中始终使用 `EnqueueSummaryJob` 而不是 `CreateSessionSummary`，以避免阻塞对话流程。

3. **监控队列大小**：如果频繁看到"queue is full"警告，请增加 `WithSummaryQueueSize` 或 `WithAsyncSummaryNum`。

4. **自定义提示词**：根据应用需求定制摘要提示词。例如，如果你正在构建客户支持 Agent，应关注关键问题和解决方案。

5. **平衡字数限制**：设置 `WithMaxSummaryWords` 以在保留上下文和减少 token 使用之间取得平衡。典型值范围为 100-300 字。

6. **测试触发条件**：尝试不同的 `WithChecksAny` 和 `WithChecksAll` 组合，找到摘要频率和成本之间的最佳平衡。

### 性能考虑

- **LLM 成本**：每次摘要生成都会调用 LLM。监控触发条件以平衡成本和上下文保留。
- **内存使用**：摘要与事件一起存储。配置适当的 TTL 以管理长时间运行会话中的内存。
- **异步 Worker**：更多 worker 会提高吞吐量但消耗更多资源。从 2-4 个 worker 开始，根据负载进行扩展。
- **队列容量**：根据预期的并发量和摘要生成时间调整队列大小。

### 完整示例

以下是演示所有组件如何协同工作的完整示例：

```go
package main

import (
    "context"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

func main() {
    ctx := context.Background()

    // 创建用于聊天和摘要的 LLM 模型
    llm, _ := openai.NewModel(
        openai.WithAPIKey("your-api-key"),
        openai.WithModelName("gpt-4"),
    )

    // 创建带灵活触发条件的摘要器
    summarizer := summary.NewSummarizer(
        llm,
        summary.WithMaxSummaryWords(200),
        summary.WithChecksAny(
            summary.CheckEventThreshold(20),
            summary.CheckTokenThreshold(4000),
            summary.CheckTimeThreshold(5*time.Minute),
        ),
    )

    // 创建带摘要器的会话服务
    sessionService := inmemory.NewSessionService(
        inmemory.WithSummarizer(summarizer),
        inmemory.WithAsyncSummaryNum(2),
        inmemory.WithSummaryQueueSize(100),
        inmemory.WithSummaryJobTimeout(30*time.Second),
    )

    // 创建启用摘要注入的 agent
    agent := llmagent.New(
        "my-agent",
        llmagent.WithModel(llm),
        llmagent.WithAddSessionSummary(true),
        llmagent.WithMaxHistoryRuns(10),
    )

    // 创建 runner
    r := runner.NewRunner("my-app", agent,
        runner.WithSessionService(sessionService))

    // 运行对话 - 摘要会自动管理
    userMsg := model.NewUserMessage("跟我讲讲 AI")
    eventChan, _ := r.Run(ctx, "user123", "session456", userMsg)

    // 消费事件
    for event := range eventChan {
        // 处理事件...
    }
}
```

## 参考资源

- [会话示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner)
- [摘要示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary)

通过合理使用会话管理功能，结合会话摘要机制，你可以构建有状态的智能 Agent，在保持对话上下文的同时高效管理内存，为用户提供连续、个性化的交互体验，同时确保系统长期运行的可持续性。
