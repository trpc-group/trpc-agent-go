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
- **多存储后端**：支持内存、Redis、PostgreSQL、MySQL 存储
- **并发安全**：内置读写锁保证并发访问安全
- **自动管理**：集成 Runner 后自动处理会话创建、加载和更新
- **软删除支持**：PostgreSQL/MySQL 支持软删除，数据可恢复

## 快速开始

### 集成到 Runner

tRPC-Agent-Go 的会话管理通过 `runner.WithSessionService` 集成到 Runner 中，Runner 会自动处理会话的创建、加载、更新和持久化。

**支持的存储后端：** 内存（Memory）、Redis、PostgreSQL、MySQL

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
            summary.CheckTimeThreshold(5*time.Minute), // 5 分钟无活动后触发
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

## 核心能力详解

### 1️⃣ 上下文管理

会话管理的核心功能是维护对话上下文，确保 Agent 能够记住历史交互并基于历史进行智能响应。

**工作原理：**

- 自动保存每轮对话的用户输入和 AI 响应
- 在新对话开始时自动加载历史事件
- Runner 自动将历史上下文注入到 LLM 输入中

**默认行为：** 通过 Runner 集成后，上下文管理完全自动化，无需手动干预。

### 2️⃣ 会话摘要（Summary）

随着对话持续增长，维护完整的事件历史可能会占用大量内存，并可能超出 LLM 的上下文窗口限制。会话摘要功能使用 LLM 自动将历史对话压缩为简洁的摘要，在保留重要上下文的同时显著降低内存占用和 token 消耗。

**核心特性：**

- **自动触发**：根据事件数量、token 数量或时间阈值自动生成摘要
- **增量处理**：只处理自上次摘要以来的新事件，避免重复计算
- **LLM 驱动**：使用配置的 LLM 模型生成高质量、上下文感知的摘要
- **非破坏性**：原始事件完整保留，摘要单独存储
- **异步处理**：后台异步执行，不阻塞对话流程
- **灵活配置**：支持自定义触发条件、提示词和字数限制

**快速配置：**

```go
import (
    "time"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// 1. 创建摘要器
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithChecksAny(                         // 任一条件满足即触发
        summary.CheckEventThreshold(20),           // 超过 20 个事件后触发
        summary.CheckTokenThreshold(4000),         // 超过 4000 个 token 后触发
        summary.CheckTimeThreshold(5*time.Minute), // 5 分钟无活动后触发
    ),
    summary.WithMaxSummaryWords(200),              // 限制摘要在 200 字以内
)

// 2. 配置会话服务
sessionService := inmemory.NewSessionService(
    inmemory.WithSummarizer(summarizer),
    inmemory.WithAsyncSummaryNum(2),               // 2 个异步 worker
    inmemory.WithSummaryQueueSize(100),            // 队列大小 100
)

// 3. 启用摘要注入到 Agent
llmAgent := llmagent.New(
    "my-agent",
    llmagent.WithModel(llm),
    llmagent.WithAddSessionSummary(true),          // 启用摘要注入
)

// 4. 创建 Runner
r := runner.NewRunner("my-agent", llmAgent,
    runner.WithSessionService(sessionService))
```

#### 摘要前后置 Hook

可以通过 Hook 调整摘要输入或输出：

```go
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithPreSummaryHook(func(ctx *summary.PreSummaryHookContext) error {
        // 可在摘要前修改 ctx.Text 或 ctx.Events
        return nil
    }),
    summary.WithPostSummaryHook(func(ctx *summary.PostSummaryHookContext) error {
        // 可在摘要返回前修改 ctx.Summary
        return nil
    }),
    summary.WithSummaryHookAbortOnError(true), // Hook 报错时中断（可选）。
)
```

说明：

- Pre-hook 主要修改 `ctx.Text`，也可调整 `ctx.Events`；Post-hook 可修改 `ctx.Summary`。
- 默认忽略 Hook 错误，需中断时使用 `WithSummaryHookAbortOnError(true)`。

**上下文注入机制：**

启用摘要后，框架会将摘要作为独立的系统消息插入到第一个现有系统消息之后，同时包含摘要时间点之后的所有增量事件，保证完整上下文：

```
When AddSessionSummary = true:
┌─────────────────────────────────────────┐
│ Existing System Message (optional)      │ ← 如果存在
├─────────────────────────────────────────┤
│ Session Summary (system message)        │ ← 插入到第一个系统消息之后
├─────────────────────────────────────────┤
│ Event 1 (after summary)                 │ ┐
│ Event 2                                 │ │
│ Event 3                                 │ │ 摘要后的所有增量事件
│ ...                                     │ │ （完整保留）
│ Event N (current message)               │ ┘
└─────────────────────────────────────────┘

When AddSessionSummary = false:
┌─────────────────────────────────────────┐
│ System Prompt                           │
├─────────────────────────────────────────┤
│ Event N-k+1                             │ ┐
│ Event N-k+2                             │ │ 最近 k 轮对话
│ ...                                     │ │ （当 MaxHistoryRuns=k 时）
│ Event N (current message)               │ ┘
└─────────────────────────────────────────┘
```

**重要提示：** 启用 `WithAddSessionSummary(true)` 时，`WithMaxHistoryRuns` 参数将被忽略，摘要后的所有事件都会完整保留。

详细配置和高级用法请参见 [会话摘要](#会话摘要) 章节。

### 3️⃣ 事件限制（EventLimit）

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

| 场景      | 推荐值    | 说明                             |
| --------- | --------- | -------------------------------- |
| 短期对话  | 100-200   | 客服咨询、单次任务               |
| 中期会话  | 500-1000  | 日常助手、多轮协作               |
| 长期会话  | 1000-2000 | 个人助理、持续项目（需配合摘要） |
| 调试/测试 | 50-100    | 快速验证，减少干扰               |

### 4️⃣ TTL 管理（自动过期）

支持为会话数据设置生存时间（Time To Live），自动清理过期数据。

**支持的 TTL 类型：**

- **SessionTTL**：会话状态和事件的过期时间
- **AppStateTTL**：应用级状态的过期时间
- **UserStateTTL**：用户级状态的过期时间

**配置示例：**

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionTTL(30*time.Minute),     // 会话 30 分钟无活动后过期
    inmemory.WithAppStateTTL(24*time.Hour),      // 应用状态 24 小时后过期
    inmemory.WithUserStateTTL(7*24*time.Hour),   // 用户状态 7 天后过期
)
```

**过期行为：**

| 存储类型   | 过期机制                   | 自动清理 |
| ---------- | -------------------------- | -------- |
| 内存存储   | 定期扫描 + 访问时检查      | 是       |
| Redis 存储 | Redis 原生 TTL             | 是       |
| PostgreSQL | 定期扫描（软删除或硬删除） | 是       |
| MySQL      | 定期扫描（软删除或硬删除） | 是       |

## 存储后端对比

tRPC-Agent-Go 提供四种会话存储后端，满足不同场景需求：

| 存储类型   | 适用场景           | 优势                              | 劣势                     |
| ---------- | ------------------ | --------------------------------- | ------------------------ |
| 内存存储   | 开发测试、小规模   | 简单快速、无需外部依赖            | 数据不持久、不支持分布式 |
| Redis 存储 | 生产环境、分布式   | 高性能、支持分布式、自动过期      | 需要 Redis 服务          |
| PostgreSQL | 生产环境、复杂查询 | 关系型数据库、支持复杂查询、JSONB | 相对较重、需要数据库     |
| MySQL      | 生产环境、复杂查询 | 广泛使用、支持复杂查询、JSON      | 相对较重、需要数据库     |

## 内存存储（Memory）

适用于开发环境和小规模应用，无需外部依赖，开箱即用。

### 配置选项

- **`WithSessionEventLimit(limit int)`**：设置每个会话存储的最大事件数量。默认值为 1000，超过限制时淘汰老的事件。
- **`WithSessionTTL(ttl time.Duration)`**：设置会话状态和事件列表的 TTL。默认值为 0（不过期）。
- **`WithAppStateTTL(ttl time.Duration)`**：设置应用级状态的 TTL。默认值为 0（不过期）。
- **`WithUserStateTTL(ttl time.Duration)`**：设置用户级状态的 TTL。默认值为 0（不过期）。
- **`WithCleanupInterval(interval time.Duration)`**：设置过期数据自动清理的间隔。默认值为 0（自动确定），如果配置了任何 TTL，默认清理间隔为 5 分钟。
- **`WithSummarizer(s summary.SessionSummarizer)`**：注入会话摘要器。
- **`WithAsyncSummaryNum(num int)`**：设置摘要处理 worker 数量。默认值为 3。
- **`WithSummaryQueueSize(size int)`**：设置摘要任务队列大小。默认值为 100。
- **`WithSummaryJobTimeout(timeout time.Duration)`**：设置单个摘要任务超时时间。默认值为 30 秒。

### 基础配置示例

```go
import "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

// 默认配置（开发环境）
sessionService := inmemory.NewSessionService()
// 效果：
// - 每个会话最多 1000 个事件
// - 所有数据永不过期
// - 不执行自动清理

// 生产环境配置
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(500),
    inmemory.WithSessionTTL(30*time.Minute),
    inmemory.WithAppStateTTL(24*time.Hour),
    inmemory.WithUserStateTTL(7*24*time.Hour),
    inmemory.WithCleanupInterval(10*time.Minute),
)
// 效果：
// - 每个会话最多 500 个事件
// - 会话 30 分钟无活动后过期
// - 应用状态 24 小时过期
// - 用户状态 7 天过期
// - 每 10 分钟清理一次过期数据
```

### 配合摘要使用

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// 创建摘要器
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithEventThreshold(20),
    summary.WithMaxSummaryWords(200),
)

// 创建会话服务并注入摘要器
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(1000),
    inmemory.WithSummarizer(summarizer),
    inmemory.WithAsyncSummaryNum(2),
    inmemory.WithSummaryQueueSize(100),
    inmemory.WithSummaryJobTimeout(30*time.Second),
)
```

## Redis 存储

适用于生产环境和分布式应用，提供高性能和自动过期能力。

### 配置选项

- **`WithRedisClientURL(url string)`**：通过 URL 创建 Redis 客户端。格式：`redis://[username:password@]host:port[/database]`。
- **`WithRedisInstance(instanceName string)`**：使用预配置的 Redis 实例。注意：`WithRedisClientURL` 的优先级高于 `WithRedisInstance`。
- **`WithSessionEventLimit(limit int)`**：设置每个会话存储的最大事件数量。默认值为 1000。
- **`WithSessionTTL(ttl time.Duration)`**：设置会话状态和事件的 TTL。默认值为 0（不过期）。
- **`WithAppStateTTL(ttl time.Duration)`**：设置应用级状态的 TTL。默认值为 0（不过期）。
- **`WithUserStateTTL(ttl time.Duration)`**：设置用户级状态的 TTL。默认值为 0（不过期）。
- **`WithEnableAsyncPersist(enable bool)`**：启用异步持久化。默认值为 `false`。
- **`WithAsyncPersisterNum(num int)`**：异步持久化 worker 数量。默认值为 10。
- **`WithSummarizer(s summary.SessionSummarizer)`**：注入会话摘要器。
- **`WithAsyncSummaryNum(num int)`**：设置摘要处理 worker 数量。默认值为 3。
- **`WithSummaryQueueSize(size int)`**：设置摘要任务队列大小。默认值为 100。
- **`WithSummaryJobTimeout(timeout time.Duration)`**：设置单个摘要任务超时时间。默认值为 30 秒。
- **`WithExtraOptions(extraOptions ...interface{})`**：为 Redis 客户端设置额外选项。

### 基础配置示例

```go
import "trpc.group/trpc-go/trpc-agent-go/session/redis"

// 使用 URL 创建（推荐）
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

### 配置复用

如果多个组件需要使用同一 Redis 实例，可以注册后复用：

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage"
    "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

// 注册 Redis 实例
redisURL := "redis://127.0.0.1:6379"
storage.RegisterRedisInstance("my-redis-instance",
    storage.WithClientBuilderURL(redisURL))

// 在会话服务中使用
sessionService, err := redis.NewService(
    redis.WithRedisInstance("my-redis-instance"),
    redis.WithSessionEventLimit(500),
)
```

### 配合摘要使用

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

### 存储结构

```
# 应用数据
appdata:{appName} -> Hash {key: value}

# 用户数据
userdata:{appName}:{userID} -> Hash {key: value}

# 会话数据
session:{appName}:{userID} -> Hash {sessionID: SessionData(JSON)}

# 事件记录
events:{appName}:{userID}:{sessionID} -> SortedSet {score: timestamp, value: Event(JSON)}

# 摘要数据（可选）
summary:{appName}:{userID}:{sessionID}:{filterKey} -> String (JSON)
```

## PostgreSQL 存储

适用于生产环境和需要复杂查询的应用，提供关系型数据库的完整能力。

### 配置选项

**连接配置：**

方式一：

- **`WithPostgresClientDSN(dsn string)`**：PostgreSQL DSN。 示例：`postgres://user:password@localhost:5432/dbname`

方式二：

- **`WithHost(host string)`**：PostgreSQL 服务器地址。默认值为 `localhost`。
- **`WithPort(port int)`**：PostgreSQL 服务器端口。默认值为 `5432`。
- **`WithUser(user string)`**：数据库用户名。默认值为 `postgres`。
- **`WithPassword(password string)`**：数据库密码。默认值为空字符串。
- **`WithDatabase(database string)`**：数据库名称。默认值为 `postgres`。
- **`WithSSLMode(sslMode string)`**：SSL 模式。默认值为 `disable`。可选值：`disable`、`require`、`verify-ca`、`verify-full`。

方式三：

- **`WithPostgresInstance(name string)`**：使用预配置的 PostgreSQL 实例。

优先级：方式一 > 方式二 > 方式三

**会话配置：**

- **`WithSessionEventLimit(limit int)`**：每个会话最大事件数量。默认值为 1000。
- **`WithSessionTTL(ttl time.Duration)`**：会话 TTL。默认值为 0（不过期）。
- **`WithAppStateTTL(ttl time.Duration)`**：应用状态 TTL。默认值为 0（不过期）。
- **`WithUserStateTTL(ttl time.Duration)`**：用户状态 TTL。默认值为 0（不过期）。
- **`WithCleanupInterval(interval time.Duration)`**：TTL 清理间隔。默认值为 5 分钟。
- **`WithSoftDelete(enable bool)`**：启用或禁用软删除。默认值为 `true`。

**异步持久化配置：**

- **`WithEnableAsyncPersist(enable bool)`**：启用异步持久化。默认值为 `false`。
- **`WithAsyncPersisterNum(num int)`**：异步持久化 worker 数量。默认值为 10。

**摘要配置：**

- **`WithSummarizer(s summary.SessionSummarizer)`**：注入会话摘要器。
- **`WithAsyncSummaryNum(num int)`**：摘要处理 worker 数量。默认值为 3。
- **`WithSummaryQueueSize(size int)`**：摘要任务队列大小。默认值为 100。
- **`WithSummaryJobTimeout(timeout time.Duration)`**：设置单个摘要任务超时时间。默认值为 30 秒。

**Schema 和表配置：**

- **`WithSchema(schema string)`**：指定 schema 名称。
- **`WithTablePrefix(prefix string)`**：表名前缀。
- **`WithSkipDBInit(skip bool)`**：跳过自动建表。

### 基础配置示例

```go
import "trpc.group/trpc-go/trpc-agent-go/session/postgres"

// 默认配置（最简）
sessionService, err := postgres.NewService(
    postgres.WithPostgresClientDSN("postgres://user:password@localhost:5432/mydb?sslmode=disable"),
)


// 生产环境完整配置
sessionService, err := postgres.NewService(
    postgres.WithPostgresClientDSN("postgres://user:password@localhost:5432/trpc_sessions?sslmode=require"),

    // 会话配置
    postgres.WithSessionEventLimit(1000),
    postgres.WithSessionTTL(30*time.Minute),
    postgres.WithAppStateTTL(24*time.Hour),
    postgres.WithUserStateTTL(7*24*time.Hour),

    // TTL 清理配置
    postgres.WithCleanupInterval(10*time.Minute),
    postgres.WithSoftDelete(true),  // 软删除模式

    // 异步持久化配置
    postgres.WithAsyncPersisterNum(4),
)
// 效果：
// - 使用 SSL 加密连接
// - 会话 30 分钟无活动后过期
// - 每 10 分钟清理过期数据（软删除）
// - 4 个异步 worker 处理写入
```

### 配置复用

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
    sessionpg "trpc.group/trpc-go/trpc-agent-go/session/postgres"
)

// 注册 PostgreSQL 实例
postgres.RegisterPostgresInstance("my-postgres-instance",
    postgres.WithClientConnString("postgres://user:password@localhost:5432/trpc_sessions?sslmode=disable"),
)

// 在会话服务中使用
sessionService, err := sessionpg.NewService(
    sessionpg.WithPostgresInstance("my-postgres-instance"),
    sessionpg.WithSessionEventLimit(500),
)
```

### Schema 与表前缀

PostgreSQL 支持 schema 和表前缀配置，适用于多租户和多环境场景：

```go
// 使用 schema
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithDatabase("mydb"),
    postgres.WithSchema("my_schema"),  // 表名：my_schema.session_states
)

// 使用表前缀
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithTablePrefix("app1_"),  // 表名：app1_session_states
)

// 结合使用
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSchema("tenant_a"),
    postgres.WithTablePrefix("app1_"),  // 表名：tenant_a.app1_session_states
)
```

**表命名规则：**

| Schema      | Prefix  | 最终表名                        |
| ----------- | ------- | ------------------------------- |
| （无）      | （无）  | `session_states`                |
| （无）      | `app1_` | `app1_session_states`           |
| `my_schema` | （无）  | `my_schema.session_states`      |
| `my_schema` | `app1_` | `my_schema.app1_session_states` |

### 软删除与 TTL 清理

**软删除配置：**

```go
// 启用软删除（默认）
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSoftDelete(true),
)

// 禁用软删除（物理删除）
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSoftDelete(false),
)
```

**删除行为对比：**

| 配置               | 删除操作                        | 查询行为                                                | 数据恢复 |
| ------------------ | ------------------------------- | ------------------------------------------------------- | -------- |
| `softDelete=true`  | `UPDATE SET deleted_at = NOW()` | 查询附带 `WHERE deleted_at IS NULL`，仅返回未软删除数据 | 可恢复   |
| `softDelete=false` | `DELETE FROM ...`               | 查询所有记录                                            | 不可恢复 |

**TTL 自动清理：**

```go
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSessionTTL(30*time.Minute),      // 会话 30 分钟后过期
    postgres.WithAppStateTTL(24*time.Hour),       // 应用状态 24 小时后过期
    postgres.WithUserStateTTL(7*24*time.Hour),    // 用户状态 7 天后过期
    postgres.WithCleanupInterval(10*time.Minute), // 每 10 分钟清理一次
    postgres.WithSoftDelete(true),                // 软删除模式
)
// 清理行为：
// - softDelete=true：过期数据标记为 deleted_at = NOW()
// - softDelete=false：过期数据被物理删除
// - 查询时始终附加 `WHERE deleted_at IS NULL`，仅返回未软删除数据
```

### 配合摘要使用

```go
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithPassword("your-password"),
    postgres.WithSessionEventLimit(1000),
    postgres.WithSessionTTL(30*time.Minute),

    // 摘要配置
    postgres.WithSummarizer(summarizer),
    postgres.WithAsyncSummaryNum(2),
    postgres.WithSummaryQueueSize(100),
)
```

### 存储结构

PostgreSQL 使用关系型表结构，JSON 数据使用 JSONB 类型存储：

```sql
-- 会话状态表
CREATE TABLE session_states (
    id BIGSERIAL PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    state JSONB,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP,
    deleted_at TIMESTAMP
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
    event JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP,
    deleted_at TIMESTAMP
);

-- 轨迹事件表
CREATE TABLE session_track_events (
    id BIGSERIAL PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    track VARCHAR(255) NOT NULL,
    event JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
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
    summary JSONB NOT NULL,
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
    value TEXT DEFAULT NULL,
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
    value TEXT DEFAULT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP,
    deleted_at TIMESTAMP,
    UNIQUE(app_name, user_id, key)
);
```

## MySQL 存储

适用于生产环境和需要复杂查询的应用，MySQL 是广泛使用的关系型数据库。

### 配置选项

**连接配置：**

- **`WithMySQLClientDSN(dsn string)`**：MySQL 连接配置
- **`WithInstanceName(name string)`**：使用预配置的 MySQL 实例。

**会话配置：**

- **`WithSessionEventLimit(limit int)`**：每个会话最大事件数量。默认值为 1000。
- **`WithSessionTTL(ttl time.Duration)`**：会话 TTL。默认值为 0（不过期）。
- **`WithAppStateTTL(ttl time.Duration)`**：应用状态 TTL。默认值为 0（不过期）。
- **`WithUserStateTTL(ttl time.Duration)`**：用户状态 TTL。默认值为 0（不过期）。
- **`WithCleanupInterval(interval time.Duration)`**：TTL 清理间隔。默认值为 5 分钟。
- **`WithSoftDelete(enable bool)`**：启用或禁用软删除。默认值为 `true`。

**异步持久化配置：**

- **`WithEnableAsyncPersist(enable bool)`**：启用异步持久化。默认值为 `false`。
- **`WithAsyncPersisterNum(num int)`**：异步持久化 worker 数量。默认值为 10。

**摘要配置：**

- **`WithSummarizer(s summary.SessionSummarizer)`**：注入会话摘要器。
- **`WithAsyncSummaryNum(num int)`**：摘要处理 worker 数量。默认值为 3。
- **`WithSummaryQueueSize(size int)`**：摘要任务队列大小。默认值为 100。
- **`WithSummaryJobTimeout(timeout time.Duration)`**：设置单个摘要任务超时时间。默认值为 30 秒。

**表配置：**

- **`WithTablePrefix(prefix string)`**：表名前缀。
- **`WithSkipDBInit(skip bool)`**：跳过自动建表。

### 基础配置示例

```go
import "trpc.group/trpc-go/trpc-agent-go/session/mysql"

// 默认配置（最简）
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
)
// 效果：
// - 连接 localhost:3306，数据库 trpc_sessions
// - 每个会话最多 1000 个事件
// - 数据永不过期
// - 2 个异步持久化 worker

// 生产环境完整配置
sessionService, err := mysql.NewService(
    // 连接配置
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),

    // 会话配置
    mysql.WithSessionEventLimit(1000),
    mysql.WithSessionTTL(30*time.Minute),
    mysql.WithAppStateTTL(24*time.Hour),
    mysql.WithUserStateTTL(7*24*time.Hour),

    // TTL 清理配置
    mysql.WithCleanupInterval(10*time.Minute),
    mysql.WithSoftDelete(true),  // 软删除模式

    // 异步持久化配置
    mysql.WithAsyncPersisterNum(4),
)
// 效果：
// - 会话 30 分钟无活动后过期
// - 每 10 分钟清理过期数据（软删除）
// - 4 个异步 worker 处理写入
```

### 配置复用

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
    sessionmysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
)

// 注册 MySQL 实例
mysql.RegisterMySQLInstance("my-mysql-instance",
    mysql.WithClientBuilderDSN("root:password@tcp(localhost:3306)/trpc_sessions?parseTime=true&charset=utf8mb4"),
)

// 在会话服务中使用
sessionService, err := sessionmysql.NewService(
    sessionmysql.WithMySQLInstance("my-mysql-instance"),
    sessionmysql.WithSessionEventLimit(500),
)
```

### 表前缀

MySQL 支持表前缀配置，适用于多应用共享数据库的场景：

```go
// 使用表前缀
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithTablePrefix("app1_"),  // 表名：app1_session_states
)
```

### 软删除与 TTL 清理

**软删除配置：**

```go
// 启用软删除（默认）
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSoftDelete(true),
)

// 禁用软删除（物理删除）
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSoftDelete(false),
)
```

**删除行为对比：**

| 配置               | 删除操作                        | 查询行为                                                | 数据恢复 |
| ------------------ | ------------------------------- | ------------------------------------------------------- | -------- |
| `softDelete=true`  | `UPDATE SET deleted_at = NOW()` | 查询附带 `WHERE deleted_at IS NULL`，仅返回未软删除数据 | 可恢复   |
| `softDelete=false` | `DELETE FROM ...`               | 查询所有记录                                            | 不可恢复 |

**TTL 自动清理：**

```go
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSessionTTL(30*time.Minute),      // 会话 30 分钟后过期
    mysql.WithAppStateTTL(24*time.Hour),       // 应用状态 24 小时后过期
    mysql.WithUserStateTTL(7*24*time.Hour),    // 用户状态 7 天后过期
    mysql.WithCleanupInterval(10*time.Minute), // 每 10 分钟清理一次
    mysql.WithSoftDelete(true),                // 软删除模式
)
// 清理行为：
// - softDelete=true：过期数据标记为 deleted_at = NOW()
// - softDelete=false：过期数据被物理删除
// - 查询时始终附加 `WHERE deleted_at IS NULL`，仅返回未软删除数据
```

### 配合摘要使用

```go
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSessionEventLimit(1000),
    mysql.WithSessionTTL(30*time.Minute),

    // 摘要配置
    mysql.WithSummarizer(summarizer),
    mysql.WithAsyncSummaryNum(2),
    mysql.WithSummaryQueueSize(100),
)
```

### 存储结构

MySQL 使用关系型表结构，JSON 数据使用 JSON 类型存储：

```sql
-- 会话状态表
CREATE TABLE session_states (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    state JSON,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NULL,
    deleted_at TIMESTAMP NULL,
    UNIQUE KEY idx_session_states_unique (app_name, user_id, session_id, deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 会话事件表
CREATE TABLE session_events (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    event JSON NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NULL,
    deleted_at TIMESTAMP NULL,
    KEY idx_session_events (app_name, user_id, session_id, deleted_at, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 会话摘要表
CREATE TABLE session_summaries (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(255) NOT NULL,
    filter_key VARCHAR(255) NOT NULL,
    summary JSON NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NULL,
    deleted_at TIMESTAMP NULL,
    UNIQUE KEY idx_session_summaries_unique (app_name, user_id, session_id, filter_key, deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 应用状态表
CREATE TABLE app_states (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    `key` VARCHAR(255) NOT NULL,
    value TEXT DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NULL,
    deleted_at TIMESTAMP NULL,
    UNIQUE KEY idx_app_states_unique (app_name, `key`, deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 用户状态表
CREATE TABLE user_states (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    app_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    `key` VARCHAR(255) NOT NULL,
    value TEXT DEFAULT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NULL,
    deleted_at TIMESTAMP NULL,
    UNIQUE KEY idx_user_states_unique (app_name, user_id, `key`, deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

**MySQL 与 PostgreSQL 的关键差异：**

- MySQL 不支持 `WHERE deleted_at IS NULL` 的 partial index，需要将 `deleted_at` 包含在唯一索引中
- MySQL 使用 `JSON` 类型而非 `JSONB`（功能类似，但存储格式不同）
- MySQL 使用 `ON DUPLICATE KEY UPDATE` 语法实现 UPSERT

## 高级用法

### Hook 能力（Append/Get）

- **AppendEventHook**：事件写入前的拦截/修改/终止。可用于内容安全、审计打标（如写入 `violation=<word>`），或直接阻断存储。关于 filterKey 的赋值请见下文“会话摘要 / FilterKey 与 AppendEventHook”。
- **GetSessionHook**：会话读取后的拦截/修改/过滤。可用来剔除带特定标签的事件，或动态补充返回的 Session 状态。
- **责任链执行**：Hook 通过 `next()` 形成链式调用，可提前返回以短路后续逻辑，错误会向上传递。
- **跨后端一致**：内存、Redis、MySQL、PostgreSQL 实现已统一接入 Hook，构造服务时注入 Hook 切片即可。
- **示例**：见 `examples/session/hook`（[代码](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/hook)）

### 直接使用 Session Service API

在大多数情况下，您应该通过 Runner 使用会话管理，Runner 会自动处理所有细节。但在某些特殊场景下（如会话管理后台、数据迁移、统计分析等），您可能需要直接操作 Session Service。

**注意：** 以下 API 仅用于特殊场景，日常使用 Runner 即可。

#### 查询会话列表

```go
// 列出某个用户的所有会话
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
// 删除指定会话
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
summaryModel := openai.New("gpt-4", openai.WithAPIKey("your-api-key"))

// 创建摘要器并配置触发条件
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithChecksAny(                     // 任一条件满足即触发
        summary.CheckEventThreshold(20),       // 自上次摘要后新增 20 个事件后触发
        summary.CheckTokenThreshold(4000),     // 自上次摘要后新增 4000 个 token 后触发
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

// PostgreSQL 存储
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithPassword("your-password"),
    postgres.WithSummarizer(summarizer),
    postgres.WithAsyncSummaryNum(2),       // 2 个异步 worker
    postgres.WithSummaryQueueSize(100),    // 队列大小 100
)

// MySQL 存储
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSummarizer(summarizer),
    mysql.WithAsyncSummaryNum(2),           // 2个异步 worker
    mysql.WithSummaryQueueSize(100),        // 队列大小 100
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
    llmagent.WithMaxHistoryRuns(10),        // 当AddSessionSummary=false时限制历史轮次
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

- 事件数量超过阈值（`WithEventThreshold`）
- Token 数量超过阈值（`WithTokenThreshold`）
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

- 会话摘要作为独立的系统消息插入到第一个现有系统消息之后（如果没有系统消息则前置添加）
- 包含摘要时间点之后的**所有增量事件**（不截断）
- 保证完整上下文：浓缩历史 + 完整新对话
- **`WithMaxHistoryRuns` 参数被忽略**

**上下文结构：**

```
┌─────────────────────────────────────────┐
│ System Prompt                           │
├─────────────────────────────────────────┤
│ Session Summary (system message)        │ ← Compressed history
├─────────────────────────────────────────┤
│ Event 1 (after summary)                 │ ┐
│ Event 2                                 │ │
│ Event 3                                 │ │ New events after summary
│ ...                                     │ │ (fully retained)
│ Event N (current message)               │ ┘
└─────────────────────────────────────────┘
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
┌─────────────────────────────────────────┐
│ System Prompt                           │
├─────────────────────────────────────────┤
│ Event N-k+1                             │ ┐
│ Event N-k+2                             │ │ Last k runs
│ ...                                     │ │ (MaxHistoryRuns=k)
│ Event N (current message)               │ ┘
└─────────────────────────────────────────┘
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

- **`WithEventThreshold(eventCount int)`**：当自上次摘要后的事件数量超过阈值时触发摘要。示例：`WithEventThreshold(20)` 在自上次摘要后新增 20 个事件后触发。
- **`WithTokenThreshold(tokenCount int)`**：当自上次摘要后的 token 数量超过阈值时触发摘要。示例：`WithTokenThreshold(4000)` 在自上次摘要后新增 4000 个 token 后触发。
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
- **`WithSkipRecent(skipFunc SkipRecentFunc)`**：通过自定义函数在摘要时跳过**最近**事件。函数接收所有事件并返回应跳过的尾部事件数量，返回 0 表示不跳过。适合避免总结最近、可能不完整的对话，或实现基于时间/内容的跳过策略。

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
    summary.WithPrompt(customPrompt), // 自定义 Prompt
    summary.WithMaxSummaryWords(100), // 注入 Prompt 里面的 {max_summary_words}
    summary.WithEventThreshold(15),
)

// 跳过固定数量（兼容旧用法）
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithSkipRecent(func(_ []event.Event) int { return 2 }), // 跳过最后 2 条
    summary.WithEventThreshold(10),
)

// 跳过最近 5 分钟内的消息（时间窗口）
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithSkipRecent(func(events []event.Event) int {
        cutoff := time.Now().Add(-5 * time.Minute)
        skip := 0
        for i := len(events) - 1; i >= 0; i-- {
            if events[i].Timestamp.After(cutoff) {
                skip++
            } else {
                break
            }
        }
        return skip
    }),
    summary.WithEventThreshold(10),
)

// 仅跳过末尾的工具调用消息
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithSkipRecent(func(events []event.Event) int {
        skip := 0
        for i := len(events) - 1; i >= 0; i-- {
            if events[i].Response != nil && len(events[i].Response.Choices) > 0 &&
                events[i].Response.Choices[0].Message.Role == model.RoleTool {
                skip++
            } else {
                break
            }
        }
        return skip
    }),
    summary.WithEventThreshold(10),
)
```

#### 会话服务选项

在会话服务中配置异步摘要处理：

- **`WithSummarizer(s summary.SessionSummarizer)`**：将摘要器注入到会话服务中。
- **`WithAsyncSummaryNum(num int)`**：设置用于摘要处理的异步 worker goroutine 数量。默认为 2。更多 worker 允许更高并发但消耗更多资源。
- **`WithSummaryQueueSize(size int)`**：设置摘要任务队列的大小。默认为 100。更大的队列允许更多待处理任务但消耗更多内存。
- **`WithSummaryJobTimeout(timeout time.Duration)`**：设置处理单个摘要任务的超时时间。默认为 30 秒。

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
// 获取全量会话摘要（默认行为）
summaryText, found := sessionService.GetSessionSummaryText(ctx, sess)
if found {
    fmt.Printf("摘要：%s\n", summaryText)
}

// 获取特定 filter key 的摘要
userSummary, found := sessionService.GetSessionSummaryText(
    ctx, sess, session.WithSummaryFilterKey("user-messages"),
)
if found {
    fmt.Printf("用户消息摘要：%s\n", userSummary)
}
```

**Filter Key 支持：**

`GetSessionSummaryText` 方法支持可选的 `WithSummaryFilterKey` 选项，用于获取特定事件过滤器的摘要：

- 不提供选项时，返回全量会话摘要（`SummaryFilterKeyAllContents`）
- 提供特定 filter key 但未找到时，回退到全量会话摘要
- 如果都不存在，兜底返回任意可用的摘要

### 工作原理

1. **增量处理**：摘要器跟踪每个会话的上次摘要时间。在后续运行中，它只处理上次摘要后发生的事件。

2. **增量摘要**：新事件与先前的摘要（作为系统事件前置）组合，生成一个既包含旧上下文又包含新信息的更新摘要。

3. **触发条件评估**：在生成摘要之前，摘要器会评估配置的触发条件（基于自上次摘要后的增量事件计数、token 计数、时间阈值）。如果条件未满足且 `force=false`，则跳过摘要。

4. **异步 Worker**：摘要任务使用基于哈希的分发策略分配到多个 worker goroutine。这确保同一会话的任务按顺序处理，而不同会话可以并行处理。

5. **回退机制**：如果异步入队失败（队列已满、上下文取消或 worker 未初始化），系统会自动回退到同步处理。

### 最佳实践

1. **选择合适的阈值**：根据 LLM 的上下文窗口和对话模式设置事件/token 阈值。对于 GPT-4（8K 上下文），考虑使用 `WithTokenThreshold(4000)` 为响应留出空间。

2. **使用异步处理**：在生产环境中始终使用 `EnqueueSummaryJob` 而不是 `CreateSessionSummary`，以避免阻塞对话流程。

3. **监控队列大小**：如果频繁看到"queue is full"警告，请增加 `WithSummaryQueueSize` 或 `WithAsyncSummaryNum`。

4. **自定义提示词**：根据应用需求定制摘要提示词。例如，如果你正在构建客户支持 Agent，应关注关键问题和解决方案。

5. **平衡字数限制**：设置 `WithMaxSummaryWords` 以在保留上下文和减少 token 使用之间取得平衡。典型值范围为 100-300 字。

6. **测试触发条件**：尝试不同的 `WithChecksAny` 和 `WithChecksAll` 组合，找到摘要频率和成本之间的最佳平衡。

### 按事件类型生成摘要

在实际应用中，你可能希望为不同类型的事件生成独立的摘要。例如：

- **用户消息摘要**：总结用户的需求和问题
- **工具调用摘要**：记录使用了哪些工具和结果
- **系统事件摘要**：跟踪系统状态变化

要实现这个功能，需要为事件设置 `FilterKey` 字段来标识事件类型。

#### 使用 AppendEventHook 设置 FilterKey

推荐使用 `AppendEventHook` 在事件写入前自动设置 `FilterKey`：

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
        // 根据事件作者自动分类
        prefix := "my-app/"  // 必须添加 appName 前缀
        switch ctx.Event.Author {
        case "user":
            ctx.Event.FilterKey = prefix + "user-messages"
        case "tool":
            ctx.Event.FilterKey = prefix + "tool-calls"
        default:
            ctx.Event.FilterKey = prefix + "misc"
        }
        return next()
    }),
)
```

设置好 FilterKey 后，就可以为不同类型的事件生成独立摘要：

```go
// 为用户消息生成摘要
err := sessionService.CreateSessionSummary(ctx, sess, "my-app/user-messages", false)

// 为工具调用生成摘要
err := sessionService.CreateSessionSummary(ctx, sess, "my-app/tool-calls", false)

// 获取特定类型的摘要
userSummary, found := sessionService.GetSessionSummaryText(
    ctx, sess, session.WithSummaryFilterKey("my-app/user-messages"))
```

#### FilterKey 前缀规范

**⚠️ 重要：FilterKey 必须添加 `appName + "/"` 前缀。**

**原因：** Runner 在过滤事件时使用 `appName + "/"` 作为过滤前缀，如果 FilterKey 没有这个前缀，事件会被过滤掉，导致：

- LLM 看不到历史对话，可能重复触发工具调用
- 摘要内容不完整，丢失重要上下文

**示例：**

```go
// ✅ 正确：带 appName 前缀
evt.FilterKey = "my-app/user-messages"

// ❌ 错误：没有前缀，事件会被过滤
evt.FilterKey = "user-messages"
```

**技术细节：** 框架使用前缀匹配机制（`strings.HasPrefix`）来判断事件是否应该被包含在上下文中。详见 `ContentRequestProcessor` 的过滤逻辑。

#### 完整示例

参考以下示例查看完整的 FilterKey 使用场景：

- [examples/session/hook](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/hook) - Hook 基础用法
- [examples/summary/filterkey](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary/filterkey) - 按 FilterKey 生成摘要

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
    llm := openai.New("gpt-4", openai.WithAPIKey("your-api-key"))

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
        llmagent.WithMaxHistoryRuns(10),        // 当AddSessionSummary=false时限制历史轮次
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
