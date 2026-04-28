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
- **事件分页**：PostgreSQL/MySQL 支持 `GetSession` 历史事件分页读取
- **TTL 管理**：支持会话数据的自动过期清理
- **多存储后端**：支持内存、SQLite、Redis、PostgreSQL、PGVector、MySQL、ClickHouse 存储
- **并发安全**：内置读写锁保证并发访问安全
- **自动管理**：集成 Runner 后自动处理会话创建、加载和更新
- **软删除支持**：SQLite/PostgreSQL/PGVector/MySQL/ClickHouse 支持软删除，数据可恢复

## 快速开始

### 集成到 Runner

tRPC-Agent-Go 的会话管理通过 `runner.WithSessionService` 集成到 Runner 中，Runner 会自动处理会话的创建、加载、更新和持久化。

**支持的存储后端：** 内存（Memory）、SQLite、Redis、PostgreSQL、PGVector、MySQL、ClickHouse

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
    "trpc.group/trpc-go/trpc-agent-go/session/summary" // 可选：启用摘要功能时需要
)

func main() {
    // 1. 创建 LLM 模型
    llm := openai.New("gpt-4", openai.WithAPIKey("your-api-key"))

    // 2. （可选）创建摘要器 - 自动压缩长对话历史
    summarizer := summary.NewSummarizer(
        llm, // 使用相同的 LLM 模型生成摘要
        summary.WithChecksAny( // 任一条件满足即触发摘要
            summary.CheckEventThreshold(20),           // 超过 20 个事件后触发
            summary.CheckTokenThreshold(4000),         // 超过 4000 个 token 后触发
            summary.CheckTimeThreshold(5*time.Minute), // 在摘要检查时判断；若最近一个待摘要事件已超过 5 分钟则触发
        ),
        summary.WithMaxSummaryWords(200), // 限制摘要在 200 字以内
    )

    // 3. 创建 Session Service（可选，不配置则使用默认内存存储）
    sessionService := inmemory.NewSessionService(
        inmemory.WithSummarizer(summarizer), // 可选：注入摘要器
        inmemory.WithAsyncSummaryNum(2),     // 可选：2 个异步 worker
        inmemory.WithSummaryQueueSize(100),  // 可选：队列大小 100
    )

    // 4. 创建 Agent
    agent := llmagent.New(
        "my-agent",
        llmagent.WithModel(llm),
        llmagent.WithInstruction("你是一个智能助手"),
        llmagent.WithAddSessionSummary(true), // 可选：启用摘要注入到上下文
        // 可选：只压缩 tool result 内容，不生成摘要；和 session summary / token tailoring 分层独立
        llmagent.WithEnableContextCompaction(true), // Pass 1 + Pass 2 的总开关
        // 配合 WithAddSessionSummary(true) 时，还会在必要时多一次同步摘要重试
        llmagent.WithContextCompactionToolResultMaxTokens(1024),  // 旧 tool result → 占位符
        // Pass 2 默认关闭，需要显式设置一个正阈值才会生效（推荐 8192）
        llmagent.WithContextCompactionOversizedToolResultMaxTokens(8192),  // 超大 result → 首尾保留截断
        llmagent.WithContextCompactionKeepRecentRequests(1),
        // 注意：WithAddSessionSummary(true) 时会忽略 WithMaxHistoryRuns 配置
        // 摘要会包含所有历史，增量事件会完整保留
    )

    // 5. 创建 Runner 并注入 Session Service
    r := runner.NewRunner(
        "my-agent",
        agent,
        runner.WithSessionService(sessionService),
    )

    // 6. 第一次对话
    ctx := context.Background()
    userMsg1 := model.NewUserMessage("我叫张三")
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
            // 流式输出，优先使用 Delta.Content，否则使用 Message.Content
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

    // 7. 第二次对话 - 自动加载历史，AI 能记住用户名字
    userMsg2 := model.NewUserMessage("我叫什么名字？")
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
            // 流式输出，优先使用 Delta.Content，否则使用 Message.Content
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
    fmt.Println() // 输出：你叫张三
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
// 限制每个会话最多保存 500 个事件
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

**TTL 刷新行为：**

TTL 仅在**写操作**时刷新（如 CreateSession、AppendEvent、UpdateSessionState 等）。读操作（GetSession）**不会**刷新 TTL。

**过期行为：**

| 存储类型 | 过期机制 | 自动清理 |
| --- | --- | --- |
| 内存存储 | 定期扫描 + 访问时检查 | 是 |
| SQLite | 定期扫描（软删除或硬删除） | 是 |
| Redis 存储 | Redis 原生 TTL | 是 |
| PostgreSQL | 定期扫描（软删除或硬删除） | 是 |
| PGVector | 定期扫描（软删除或硬删除） | 是 |
| MySQL | 定期扫描（软删除或硬删除） | 是 |
| ClickHouse | 应用层清理 + Native TTL | 是 |

## 存储后端对比

tRPC-Agent-Go 提供七种会话存储后端，满足不同场景需求：

| 存储类型 | 适用场景 | 持久化 | 分布式 | 复杂查询 |
| --- | --- | --- | --- | --- |
| [内存存储](inmemory.md) | 开发测试、小规模 | ❌ | ❌ | ❌ |
| [SQLite](sqlite.md) | 本地持久化、单机 | ✅ | ❌ | ✅ |
| [Redis 存储](redis.md) | 生产环境、分布式 | ✅ | ✅ | ❌ |
| [PostgreSQL](postgres.md) | 生产环境、复杂查询 | ✅ | ✅ | ✅ |
| [PGVector](pgvector.md) | 生产环境、语义召回 | ✅ | ✅ | ✅ |
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
        // 存储前进行内容过滤
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
        // 读取后过滤事件
        sess.Events = filterEvents(sess.Events)
        return sess, nil
    }),
)
```

**责任链执行**：Hook 通过 `next()` 形成链式调用，可提前返回以短路后续逻辑，错误会向上传递。

**跨后端一致**：内存、SQLite、Redis、PostgreSQL、PGVector、MySQL、ClickHouse 所有存储后端均已统一接入 Hook 机制，构造服务时注入 Hook 切片即可，使用方式完全一致。

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

```go
// 仅获取会话元数据，不返回 Events 和 Tracks
sessions, err := sessionService.ListSessions(ctx, session.UserKey{
    AppName: "my-agent",
    UserID:  "user123",
}, session.WithListSessionOnlyMeta())
```

说明：

- `session.WithListSessionOnlyMeta()` 只用于 `ListSessions`
- 当前仅 `inmemory` 和 `redis` 后端支持该优化

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
// 获取完整会话
sess, err := sessionService.GetSession(ctx, session.Key{
    AppName:   "my-agent",
    UserID:    "user123",
    SessionID: "session-id-123",
})

// 获取最近 10 个事件的会话
sess, err := sessionService.GetSession(ctx, key,
    session.WithEventNum(10))

// 获取指定时间后的事件
sess, err := sessionService.GetSession(ctx, key,
    session.WithEventTime(time.Now().Add(-1*time.Hour)))

// 分页获取历史事件
// 仅 PostgreSQL / MySQL 的 GetSession 支持
// EventPage 与 EventNum / EventTime 不能同时使用
sess, err := sessionService.GetSession(ctx, key,
    session.WithGetSessionEventPage(20, 10))
```

#### 直接追加事件到会话

在某些场景下，您可能需要直接将事件追加到会话中，而不调用模型。这在以下场景中很有用：

- 从外部源预加载对话历史
- 在首次用户查询前插入系统消息或上下文
- 将用户操作或元数据记录为事件
- 以编程方式构建对话上下文

**重要提示**：Event 既可以表示用户请求，也可以表示模型响应。当您使用 `Runner.Run()` 时，框架会自动为用户消息和助手回复创建事件。

**示例：追加用户消息**

```go
import (
    "context"
    "github.com/google/uuid"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/session"
)

// 获取或创建会话
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

// 创建用户消息
message := model.NewUserMessage("你好，我正在学习 Go 编程。")

// 创建事件，必填字段：
// - invocationID: 唯一标识符（必填）
// - author: 事件作者，用户消息使用 "user"（必填）
// - response: *model.Response，包含 Choices 和 Message（必填）
invocationID := uuid.New().String()
evt := event.NewResponseEvent(
    invocationID, // 必填：唯一调用标识符
    "user",       // 必填：事件作者
    &model.Response{
        Done: false, // 推荐：非最终事件设为 false
        Choices: []model.Choice{
            {
                Index:   0,       // 必填：选择索引
                Message: message, // 必填：包含 Content 或 ContentParts 的消息
            },
        },
    },
)
evt.RequestID = uuid.New().String() // 可选：用于追踪

// 追加事件到会话
if err := sessionService.AppendEvent(ctx, sess, evt); err != nil {
    return fmt.Errorf("append event failed: %w", err)
}
```

**示例：追加系统消息**

```go
systemMessage := model.Message{
    Role:    model.RoleSystem,
    Content: "你是一个专门帮助 Go 编程的助手。",
}

evt := event.NewResponseEvent(
    uuid.New().String(),
    "system", // 系统消息的作者
    &model.Response{
        Done:    false,
        Choices: []model.Choice{{Index: 0, Message: systemMessage}},
    },
)

if err := sessionService.AppendEvent(ctx, sess, evt); err != nil {
    return err
}
```

**示例：追加助手消息**

```go
assistantMessage := model.Message{
    Role:    model.RoleAssistant,
    Content: "Go 是一种静态类型、编译型的编程语言。",
}

evt := event.NewResponseEvent(
    uuid.New().String(),
    "assistant", // 助手消息的作者（或使用 agent 名称）
    &model.Response{
        Done:    false,
        Choices: []model.Choice{{Index: 0, Message: assistantMessage}},
    },
)

if err := sessionService.AppendEvent(ctx, sess, evt); err != nil {
    return err
}
```

**Event 必填字段**

使用 `event.NewResponseEvent()` 创建事件时，以下字段是必填的：

1. **函数参数**：
   - `invocationID` (string): 唯一标识符，通常使用 `uuid.New().String()`
   - `author` (string): 事件作者（`"user"`、`"system"` 或 agent 名称）
   - `response` (*model.Response): 包含 Choices 的响应对象

2. **Response 字段**：
   - `Choices` ([]model.Choice): 至少包含一个 Choice，包含 `Index` 和 `Message`
   - `Message`: 必须包含 `Content` 或 `ContentParts`

3. **自动生成字段**（由 `event.NewResponseEvent()` 自动设置）：
   - `ID`: 自动生成的 UUID
   - `Timestamp`: 自动设置为当前时间
   - `Version`: 自动设置为 `CurrentVersion`

4. **持久化要求**：
   - `Response != nil`
   - `!IsPartial`（或包含 `StateDelta`）
   - `IsValidContent()` 返回 `true`

**与 Runner 配合使用**

当您后续使用 `Runner.Run()` 处理同一会话时：

1. Runner 会自动加载会话（包括所有已追加的事件）
2. 将会话事件转换为消息
3. 将所有消息（已追加的 + 当前的）包含在对话上下文中
4. 一起发送给模型

所有已追加的事件都会成为对话历史的一部分，并在后续交互中可供模型使用。

**示例**：见 `examples/session/appendevent`（[代码](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/appendevent)）

## Track 事件

Track 事件是 Session 中独立于主对话事件的轨迹存储机制，目前主要用于 AGUI 场景下的事件存储。它允许在会话中记录特定类型的事件，而不影响主对话流程。

**接口说明**：

Track 事件的 API 定义在 `session.TrackService` 接口上，它独立于 `session.Service`：

```go
type TrackService interface {
    AppendTrackEvent(ctx context.Context, sess *Session, event *TrackEvent, opts ...Option) error
}
```

并非所有存储后端都实现了 `TrackService`。使用时需要通过类型断言获取：

| 存储后端 | 是否实现 TrackService |
| --- | --- |
| 内存存储（inmemory） | ✅ |
| SQLite | ✅ |
| Redis 存储 | ✅ |
| PostgreSQL 存储 | ✅ |
| PGVector | ✅ |
| MySQL 存储 | ✅ |
| ClickHouse 存储 | ❌ |

**基本用法**：

```go
// 通过类型断言获取 TrackService
trackService, ok := sessionService.(session.TrackService)
if !ok {
    log.Fatal("当前存储后端不支持 TrackService")
}

// 追加 Track 事件
payload, _ := json.Marshal(map[string]any{"action": "button_click"})
err := trackService.AppendTrackEvent(ctx, sess, &session.TrackEvent{
    Track:     "ui-events",
    Payload:   payload,
    Timestamp: time.Now(),
})

// 从会话中获取 Track 事件
trackEvents, err := sess.GetTrackEvents("ui-events")
```

## 语义召回（仅 PGVector）

`session/pgvector` 后端额外实现了 `session.SearchableService`，可以按语义相似度在某个用户的一个或多个会话里检索历史消息。只有已持久化的用户/助手文本事件会被建立索引；工具调用、工具结果、partial 事件和空内容不会进入检索索引。

```go
searchSvc, ok := sessionService.(session.SearchableService)
if ok {
    hits, err := searchSvc.SearchEvents(ctx, session.EventSearchRequest{
        Query: "travel plan",
        UserKey: session.UserKey{
            AppName: "my-agent",
            UserID:  "user123",
        },
        SearchMode: session.SearchModeHybrid,
        MaxResults: 5,
    })
    _ = hits
    _ = err
}
```

如果你使用的是 LLMAgent，那么像 `session/pgvector` 这种可检索后端还可以通过
`llmagent.WithPreloadSessionRecall(...)` 把跨会话 recall 自动预加载到
system prompt 中。

更详细的配置项、索引行为和检索过滤条件，请参考 [PGVector 会话存储](pgvector.md)。

## 相关文档

- [会话摘要](summary.md) - 自动压缩长对话历史
- [内存存储](inmemory.md) - 开发测试环境
- [SQLite 存储](sqlite.md) - 本地持久化、单机
- [Redis 存储](redis.md) - 生产环境分布式存储
- [PostgreSQL 存储](postgres.md) - 关系型数据库存储
- [PGVector 会话存储](pgvector.md) - 基于 PostgreSQL 的语义会话检索
- [MySQL 存储](mysql.md) - 关系型数据库存储
- [ClickHouse 存储](clickhouse.md) - 海量数据存储

## 参考资源

- [会话示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner)
- [摘要示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary)
- [Hook 示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/hook)
