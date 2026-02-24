# Session 会话管理

## 概述

tRPC-Agent-Go 框架提供了强大的会话（Session）管理功能，用于维护 Agent 与用户交互过程中的对话历史和上下文信息。通过自动持久化对话记录、智能摘要压缩和灵活的存储后端，会话管理为构建有状态的智能 Agent 提供了完整的基础设施。

### 定位

Session 用于管理当前会话的上下文，隔离维度为 `<appName, userID, SessionID>`，保存这一段对话里的用户消息、Agent 回复、工具调用结果以及基于这些内容生成的简要摘要，用于支撑多轮问答场景。

在同一条对话中，它让多轮问答之间能够自然承接，避免用户在每一轮都重新描述同一个问题或提供相同参数。

### 🎯 核心特性

- **上下文管理**：自动加载历史对话，实现真正的多轮对话
- **会话摘要**：使用 LLM 自动压缩长对话历史，在保留关键上下文的同时显著降低 token 消耗
- **事件限制**：控制每个会话存储的最大事件数量，防止内存溢出
- **TTL 管理**：支持会话数据的自动过期清理
- **多存储后端**：支持内存、Redis、PostgreSQL、MySQL、ClickHouse 存储
- **并发安全**：内置读写锁保证并发访问安全
- **自动管理**：集成 Runner 后自动处理会话创建、加载和更新
- **软删除支持**：PostgreSQL/MySQL/ClickHouse 支持软删除，数据可恢复
- **Track 事件**：支持独立的轨迹事件存储，用于记录特定类型的事件

## 快速开始

### 集成到 Runner

tRPC-Agent-Go 的会话管理通过 `runner.WithSessionService` 集成到 Runner 中，Runner 会自动处理会话的创建、加载、更新和持久化。

**支持的存储后端：** 内存（Memory）、Redis、PostgreSQL、MySQL、ClickHouse

**默认行为：** 如果不配置 `runner.WithSessionService`，Runner 会默认使用内存存储（Memory），数据在进程重启后会丢失。

### 基础示例

```go
package main

import (
    "context"
    "fmt"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

func main() {
    // 1. Create LLM model
    llm := openai.New("gpt-4", openai.WithAPIKey("your-api-key"))

    // 2. (Optional) Create summarizer - automatically compress long conversation history
    summarizer := summary.NewSummarizer(
        llm,
        summary.WithChecksAny(
            summary.CheckEventThreshold(20),
            summary.CheckTokenThreshold(4000),
            summary.CheckTimeThreshold(5*time.Minute),
        ),
        summary.WithMaxSummaryWords(200),
    )

    // 3. Create Session Service (optional, defaults to in-memory storage if not configured)
    sessionService := inmemory.NewSessionService(
        inmemory.WithSummarizer(summarizer),
        inmemory.WithAsyncSummaryNum(2),
        inmemory.WithSummaryQueueSize(100),
    )

    // 4. Create Agent
    agent := llmagent.New(
        "my-agent",
        llmagent.WithModel(llm),
        llmagent.WithInstruction("You are an intelligent assistant"),
        llmagent.WithAddSessionSummary(true),
    )

    // 5. Create Runner and inject Session Service
    r := runner.NewRunner(
        "my-agent",
        agent,
        runner.WithSessionService(sessionService),
    )

    // 6. First conversation
    ctx := context.Background()
    userMsg1 := model.NewUserMessage("My name is John")
    eventChan, err := r.Run(ctx, "user123", "session-001", userMsg1)
    if err != nil {
        fmt.Printf("Error: %v\n", err)
        return
    }
    fmt.Print("AI: ")
    for event := range eventChan {
        if event == nil || event.Response == nil {
            continue
        }
        if event.Response.Error != nil {
            fmt.Printf("\nError: %s (type: %s)\n", event.Response.Error.Message, event.Response.Error.Type)
            continue
        }
        if len(event.Response.Choices) > 0 {
            choice := event.Response.Choices[0]
            if choice.Delta.Content != "" {
                fmt.Print(choice.Delta.Content)
            } else if choice.Message.Content != "" {
                fmt.Print(choice.Message.Content)
            }
        }
        if event.IsFinalResponse() {
            break
        }
    }
    fmt.Println()

    // 7. Second conversation - automatically loads history, AI remembers user's name
    userMsg2 := model.NewUserMessage("What's my name?")
    eventChan, err = r.Run(ctx, "user123", "session-001", userMsg2)
    if err != nil {
        fmt.Printf("Error: %v\n", err)
        return
    }
    fmt.Print("AI: ")
    for event := range eventChan {
        if event == nil || event.Response == nil {
            continue
        }
        if event.Response.Error != nil {
            fmt.Printf("\nError: %s (type: %s)\n", event.Response.Error.Message, event.Response.Error.Type)
            continue
        }
        if len(event.Response.Choices) > 0 {
            choice := event.Response.Choices[0]
            if choice.Delta.Content != "" {
                fmt.Print(choice.Delta.Content)
            } else if choice.Message.Content != "" {
                fmt.Print(choice.Message.Content)
            }
        }
        if event.IsFinalResponse() {
            break
        }
    }
    fmt.Println() // Output: Your name is John
}
```

### Runner 自动提供的能力

集成 Session Service 后，Runner 会自动提供以下能力，**无需手动调用任何 Session API**：

1. **自动会话创建**：首次对话时自动创建会话（如果 SessionID 为空则生成 UUID）
2. **自动会话加载**：每次对话开始时自动加载历史上下文
3. **自动会话更新**：对话结束后自动保存新的事件
4. **上下文连续性**：自动将历史对话注入到 LLM 输入，实现多轮对话
5. **自动摘要生成**（可选）：满足触发条件时后台异步生成摘要，无需手动干预

## 核心概念

### Session 结构

Session 是会话管理的核心数据结构，包含以下字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `ID` | `string` | 会话 ID |
| `AppName` | `string` | 应用名称 |
| `UserID` | `string` | 用户 ID |
| `State` | `StateMap` | 会话状态（键值对） |
| `Events` | `[]event.Event` | 会话事件列表 |
| `Tracks` | `map[Track]*TrackEvents` | Track 事件映射 |
| `Summaries` | `map[string]*Summary` | 会话摘要映射 |
| `UpdatedAt` | `time.Time` | 最后更新时间 |
| `CreatedAt` | `time.Time` | 创建时间 |

### Key 结构

Session 通过 `Key` 结构唯一标识：

```go
type Key struct {
    AppName   string // app name
    UserID    string // user id
    SessionID string // session id
}
```

### Service 接口

所有存储后端都实现了 `session.Service` 接口：

```go
type Service interface {
    // CreateSession creates a new session.
    CreateSession(ctx context.Context, key Key, state StateMap, options ...Option) (*Session, error)

    // GetSession gets a session.
    GetSession(ctx context.Context, key Key, options ...Option) (*Session, error)

    // ListSessions lists all sessions by user scope.
    ListSessions(ctx context.Context, userKey UserKey, options ...Option) ([]*Session, error)

    // DeleteSession deletes a session.
    DeleteSession(ctx context.Context, key Key, options ...Option) error

    // UpdateAppState updates the app-level state.
    UpdateAppState(ctx context.Context, appName string, state StateMap) error

    // DeleteAppState deletes the app-level state by key.
    DeleteAppState(ctx context.Context, appName string, key string) error

    // ListAppStates lists all app-level states.
    ListAppStates(ctx context.Context, appName string) (StateMap, error)

    // UpdateUserState updates the user-level state.
    UpdateUserState(ctx context.Context, userKey UserKey, state StateMap) error

    // ListUserStates lists all user-level states.
    ListUserStates(ctx context.Context, userKey UserKey) (StateMap, error)

    // DeleteUserState deletes the user-level state by key.
    DeleteUserState(ctx context.Context, userKey UserKey, key string) error

    // UpdateSessionState updates the session-level state directly.
    UpdateSessionState(ctx context.Context, key Key, state StateMap) error

    // AppendEvent appends an event to a session.
    AppendEvent(ctx context.Context, session *Session, event *event.Event, options ...Option) error

    // CreateSessionSummary triggers summarization for the session.
    CreateSessionSummary(ctx context.Context, sess *Session, filterKey string, force bool) error

    // EnqueueSummaryJob enqueues a summary job for asynchronous processing.
    EnqueueSummaryJob(ctx context.Context, sess *Session, filterKey string, force bool) error

    // GetSessionSummaryText returns the latest summary text for the session.
    GetSessionSummaryText(ctx context.Context, sess *Session, opts ...SummaryOption) (string, bool)

    // Close closes the service.
    Close() error
}
```

## 核心能力详解

### 1️⃣ 上下文管理

会话管理的核心功能是维护对话上下文，确保 Agent 能够记住历史交互并基于历史进行智能响应。

**工作原理：**

- 自动保存每轮对话的用户输入和 AI 响应
- 在新对话开始时自动加载历史事件
- Runner 自动将历史上下文注入到 LLM 输入中

**默认行为：** 通过 Runner 集成后，上下文管理完全自动化，无需手动干预。

### 2️⃣ 事件限制（EventLimit）

控制每个会话存储的最大事件数量，防止长时间对话导致内存溢出。

**工作机制：**

- 超过限制时自动淘汰最老的事件（FIFO）
- 只影响存储，不影响业务逻辑
- 适用于所有存储后端

**配置示例：**

```go
// Limit each session to 500 events max
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(500),
)
```

**推荐配置：**

| 场景 | 推荐值 | 说明 |
| --- | --- | --- |
| 短期对话 | 100-200 | 客服咨询、单次任务 |
| 中期会话 | 500-1000 | 日常助手、多轮协作 |
| 长期会话 | 1000-2000 | 个人助理、持续项目（需配合摘要） |
| 调试/测试 | 50-100 | 快速验证，减少干扰 |

### 3️⃣ TTL 管理（自动过期）

支持为会话数据设置生存时间（Time To Live），自动清理过期数据。

**支持的 TTL 类型：**

- **SessionTTL**：会话状态和事件的过期时间
- **AppStateTTL**：应用级状态的过期时间
- **UserStateTTL**：用户级状态的过期时间

**配置示例：**

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionTTL(30*time.Minute),
    inmemory.WithAppStateTTL(24*time.Hour),
    inmemory.WithUserStateTTL(7*24*time.Hour),
)
```

**过期行为：**

| 存储类型 | 过期机制 | 自动清理 |
| --- | --- | --- |
| 内存存储 | 定期扫描 + 访问时检查 | 是 |
| Redis 存储 | Redis 原生 TTL | 是 |
| PostgreSQL | 定期扫描（软删除或硬删除） | 是 |
| MySQL | 定期扫描（软删除或硬删除） | 是 |
| ClickHouse | 应用层清理 + Native TTL | 是 |

## 存储后端对比

tRPC-Agent-Go 提供五种会话存储后端，满足不同场景需求：

| 存储类型 | 适用场景 | 持久化 | 分布式 | 复杂查询 |
| --- | --- | --- | --- | --- |
| [内存存储](inmemory.md) | 开发测试、小规模 | ❌ | ❌ | ❌ |
| [Redis 存储](redis.md) | 生产环境、分布式 | ✅ | ✅ | ❌ |
| [PostgreSQL](postgres.md) | 生产环境、复杂查询 | ✅ | ✅ | ✅ |
| [MySQL](mysql.md) | 生产环境、复杂查询 | ✅ | ✅ | ✅ |
| [ClickHouse](clickhouse.md) | 生产环境、海量日志 | ✅ | ✅ | ✅ |

## Hook 能力

Session Service 支持 Hook 机制，允许在事件写入和会话读取时进行拦截和修改。

### AppendEventHook

事件写入前的拦截/修改/终止。可用于内容安全、审计打标，或直接阻断存储。

```go
type AppendEventContext struct {
    Context context.Context
    Session *Session
    Event   *event.Event
    Key     Key
}

type AppendEventHook func(ctx *AppendEventContext, next func() error) error
```

### GetSessionHook

会话读取后的拦截/修改/过滤。可用来剔除带特定标签的事件，或动态补充返回的 Session 状态。

```go
type GetSessionContext struct {
    Context context.Context
    Key     Key
    Options *Options
}

type GetSessionHook func(ctx *GetSessionContext, next func() (*Session, error)) (*Session, error)
```

### 使用示例

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
        // Content filtering before storage
        if containsSensitiveContent(ctx.Event) {
            return fmt.Errorf("sensitive content detected")
        }
        return next()
    }),
    inmemory.WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
        sess, err := next()
        if err != nil {
            return nil, err
        }
        // Filter events after retrieval
        sess.Events = filterEvents(sess.Events)
        return sess, nil
    }),
)
```

**责任链执行**：Hook 通过 `next()` 形成链式调用，可提前返回以短路后续逻辑，错误会向上传递。

## 高级用法

### 直接使用 Session Service API

在大多数情况下，您应该通过 Runner 使用会话管理，Runner 会自动处理所有细节。但在某些特殊场景下（如会话管理后台、数据迁移、统计分析等），您可能需要直接操作 Session Service。

#### 查询会话列表

```go
sessions, err := sessionService.ListSessions(ctx, session.UserKey{
    AppName: "my-agent",
    UserID:  "user123",
})

for _, sess := range sessions {
    fmt.Printf("SessionID: %s, Events: %d\n", sess.ID, len(sess.Events))
}
```

#### 手动删除会话

```go
err := sessionService.DeleteSession(ctx, session.Key{
    AppName:   "my-agent",
    UserID:    "user123",
    SessionID: "session-id-123",
})
```

#### 手动获取会话详情

```go
// Get full session
sess, err := sessionService.GetSession(ctx, session.Key{
    AppName:   "my-agent",
    UserID:    "user123",
    SessionID: "session-id-123",
})

// Get session with last 10 events
sess, err := sessionService.GetSession(ctx, key,
    session.WithEventNum(10))

// Get events after a specific time
sess, err := sessionService.GetSession(ctx, key,
    session.WithEventTime(time.Now().Add(-1*time.Hour)))
```

#### 直接追加事件到会话

在某些场景下，您可能需要直接将事件追加到会话中，而不调用模型。这在以下场景中很有用：

- 从外部源预加载对话历史
- 在首次用户查询前插入系统消息或上下文
- 将用户操作或元数据记录为事件
- 以编程方式构建对话上下文

```go
import (
    "context"
    "github.com/google/uuid"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/session"
)

// Get or create session
sessionKey := session.Key{
    AppName:   "my-agent",
    UserID:    "user123",
    SessionID: "session-123",
}
sess, err := sessionService.GetSession(ctx, sessionKey)
if err != nil {
    return err
}
if sess == nil {
    sess, err = sessionService.CreateSession(ctx, sessionKey, session.StateMap{})
    if err != nil {
        return err
    }
}

// Create user message
message := model.NewUserMessage("Hello, I'm learning Go programming.")

// Create event with required fields
invocationID := uuid.New().String()
evt := event.NewResponseEvent(
    invocationID,
    "user",
    &model.Response{
        Done: false,
        Choices: []model.Choice{
            {
                Index:   0,
                Message: message,
            },
        },
    },
)
evt.RequestID = uuid.New().String()

// Append event to session
if err := sessionService.AppendEvent(ctx, sess, evt); err != nil {
    return fmt.Errorf("append event failed: %w", err)
}
```

## 相关文档

- [会话摘要](summary.md) - 自动压缩长对话历史
- [Track 事件](track.md) - 独立的轨迹事件存储
- [内存存储](inmemory.md) - 开发测试环境
- [Redis 存储](redis.md) - 生产环境分布式存储
- [PostgreSQL 存储](postgres.md) - 关系型数据库存储
- [MySQL 存储](mysql.md) - 关系型数据库存储
- [ClickHouse 存储](clickhouse.md) - 海量数据存储

## 参考资源

- [会话示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner)
- [摘要示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary)
- [Hook 示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/hook)
