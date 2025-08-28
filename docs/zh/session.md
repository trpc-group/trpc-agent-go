# Session 会话管理

## 概述

tRPC-Agent-Go 框架提供了强大的会话（Session）管理功能，用于维护 Agent 与用户交互过程中的对话历史和上下文信息。会话管理模块支持多种存储后端，包括内存存储和 Redis 存储，为 Agent 应用提供了灵活的状态持久化能力。

### 🎯 核心特性

- **会话持久化**：保存完整的对话历史和上下文
- **多存储后端**：支持内存存储和 Redis 存储
- **事件追踪**：完整记录会话中的所有交互事件
- **多级存储**：支持应用级、用户级和会话级数据存储
- **并发安全**：内置读写锁保证并发访问安全
- **自动管理**：在Runner中指定Session Service后，即可自动处理会话的创建、加载和更新

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

### 集成Session Service

使用 `runner.WithSessionService` 可以为 Agent 运行器提供完整的会话管理能力，如果未指定，则默认使用基于内存的会话管理。Runner 会自动处理会话的创建、加载和更新，用户无需额外操作，也不用关心内部细节：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

// 选择会话服务类型
var sessionService session.Service

// 方式1：使用内存存储（开发测试）
sessionService = inmemory.NewSessionService()

// 方式2：使用 Redis 存储（生产环境）
sessionService, err = redis.NewService(
    redis.WithURL("redis://127.0.0.1:6379/0"),
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
    redis.WithURL("redis://localhost:6379/0"),
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

```go
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
```

#### 配置复用

如果你有多个组件需要用到redis，可以配置一个redis实例，然后在多个组件中复用配置。

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

## 参考资源

- [参考示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner)

通过合理使用会话管理功能，你可以构建有状态的智能 Agent，为用户提供连续、个性化的交互体验。
