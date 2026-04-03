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
- **多存储后端**：支持内存、SQLite、Redis、PostgreSQL、MySQL、ClickHouse 存储
- **并发安全**：内置读写锁保证并发访问安全
- **自动管理**：集成 Runner 后自动处理会话创建、加载和更新
- **软删除支持**：PostgreSQL/MySQL/SQLite 支持软删除，数据可恢复

## 快速开始

### 集成到 Runner

tRPC-Agent-Go 的会话管理通过 `runner.WithSessionService` 集成到 Runner 中，Runner 会自动处理会话的创建、加载、更新和持久化。

**支持的存储后端：** 内存（Memory）、SQLite、Redis、PostgreSQL、MySQL、ClickHouse

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

#### 模型回调（Before/After Model）

`summarizer` 在调用底层 `model.GenerateContent` 前后支持模型回调（structured 签名），可用于修改请求、短路返回自定义响应、或在摘要请求上做埋点。

```go
import (
    "context"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

callbacks := model.NewCallbacks().
    RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
        // 可修改 args.Request，也可以返回 CustomResponse 来跳过真实 model 调用
        return nil, nil
    }).
    RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
        // 可通过 CustomResponse 覆盖模型输出
        return nil, nil
    })

summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithModelCallbacks(callbacks),
)
```

**上下文注入机制：**

启用摘要后，框架会将摘要合并到已有系统消息中；如果原来没有系统消息，则会前置插入一条新的系统消息。同时保留摘要时间点之后的所有增量事件，保证完整上下文：

```text
When AddSessionSummary = true:
┌─────────────────────────────────────────┐
│ System Prompt                           │ ← 若已存在系统消息，则与摘要合并；
│ (merged with Session Summary)           │    否则前置插入新的系统消息
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

#### 摘要格式自定义

默认情况下，会话摘要会以包含上下文标签和关于优先考虑当前对话信息的提示进行格式化：

**默认格式：**

```
Here is a brief summary of your previous interactions:

<summary_of_previous_interactions>
[摘要内容]
</summary_of_previous_interactions>

Note: this information is from previous interactions and may be outdated. You should ALWAYS prefer information from this conversation over the past summary.
```

您可以使用 `WithSummaryFormatter`（在 `llmagent` 和 `graphagent` 中可用）来自定义摘要格式，以更好地匹配您的特定使用场景或模型需求。

**自定义格式示例：**

```go
// 使用简化格式的自定义格式化器
agent := llmagent.New(
    "my-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithAddSessionSummary(true),
    llmagent.WithSummaryFormatter(func(summary string) string {
        return fmt.Sprintf("## Previous Context\n\n%s", summary)
    }),
)
```

**使用场景：**

- **简化格式**：使用简洁的标题和最少的上下文提示来减少 token 消耗
- **语言本地化**：将上下文提示翻译为目标语言（例如：中文、日语）
- **角色特定格式**：为不同的 Agent 角色提供不同的格式（助手、研究员、程序员）
- **模型优化**：根据特定模型的偏好调整格式（某些模型对特定的提示结构响应更好）

**重要注意事项：**

- 格式化函数接收来自会话的原始摘要文本并返回格式化后的字符串
- 自定义格式化器应确保摘要可与其他消息清楚地区分开
- 默认格式设计为与大多数模型和使用场景兼容
- 当使用 `WithAddSessionSummary(false)` 时，格式化器**不会生效**

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
| SQLite     | 定期扫描（软删除或硬删除） | 是       |
| PostgreSQL | 定期扫描（软删除或硬删除） | 是       |
| MySQL      | 定期扫描（软删除或硬删除） | 是       |

## 存储后端对比

tRPC-Agent-Go 提供六种会话存储后端，满足不同场景需求：

| 存储类型   | 适用场景           |
| ---------- | ------------------ |
| 内存存储   | 开发测试、小规模   |
| SQLite     | 本地持久化、单机   |
| Redis 存储 | 生产环境、分布式   |
| PostgreSQL | 生产环境、复杂查询 |
| MySQL      | 生产环境、复杂查询 |
| ClickHouse | 生产环境、海量日志 |

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
- **`WithSummaryJobTimeout(timeout time.Duration)`**：设置单个摘要任务超时时间。默认值为 60 秒。

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
    inmemory.WithSummaryJobTimeout(60*time.Second),
)
```

## SQLite 存储

SQLite 是一种嵌入式数据库，数据保存在单个文件中，适合：

- 本地开发和 Demo（不需要额外部署数据库）
- 单机部署但希望进程重启后仍保留会话数据
- 轻量级 CLI/小服务的持久化

### 依赖与构建要求

该后端使用 `github.com/mattn/go-sqlite3` 驱动，需要开启 CGO（需要 C 编译器）。

### 基础配置示例

```go
import (
    "database/sql"
    "time"

    _ "github.com/mattn/go-sqlite3"
    sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

db, err := sql.Open("sqlite3", "file:sessions.db?_busy_timeout=5000")
if err != nil {
    // handle error
}

sessionService, err := sessionsqlite.NewService(
    db,
    sessionsqlite.WithSessionEventLimit(1000),
    sessionsqlite.WithSessionTTL(30*time.Minute),
    sessionsqlite.WithSoftDelete(true),
)
if err != nil {
    // handle error
}
defer sessionService.Close()
```

**注意事项**：

- `NewService` 接收 `*sql.DB`。Session Service 会在 `Close()` 时关闭该 DB，避免重复关闭。
- 如果单机并发较高，可以考虑在 DSN 中开启 WAL（例如 `_journal_mode=WAL`）并设置 `_busy_timeout`。

### 配置选项

- TTL 与清理：`WithSessionTTL`、`WithAppStateTTL`、`WithUserStateTTL`、`WithCleanupInterval`
- 保留策略：`WithSessionEventLimit`
- 异步持久化：`WithEnableAsyncPersist`、`WithAsyncPersisterNum`
- 软删除：`WithSoftDelete`（默认开启）
- 摘要：`WithSummarizer`、`WithAsyncSummaryNum`、`WithSummaryQueueSize`、`WithSummaryJobTimeout`
- DDL/命名：`WithSkipDBInit`、`WithTablePrefix`
- Hooks：`WithAppendEventHook`、`WithGetSessionHook`

## Redis 存储

适用于生产环境和分布式应用，提供高性能和自动过期能力。Redis Session Service 内部维护两套存储引擎：**HashIdx**（新，默认）和 **ZSet**（旧），通过兼容模式（CompatMode）实现平滑迁移。

### 配置选项

**连接配置：**

- **`WithRedisClientURL(url string)`**：通过 URL 创建 Redis 客户端。格式：`redis://[username:password@]host:port[/database]`。
- **`WithRedisInstance(instanceName string)`**：使用预配置的 Redis 实例。注意：`WithRedisClientURL` 的优先级高于 `WithRedisInstance`。
- **`WithExtraOptions(extraOptions ...interface{})`**：为 Redis 客户端设置额外选项，会传递给底层的 client builder。

**会话配置：**

- **`WithSessionEventLimit(limit int)`**：设置每个会话存储的最大事件数量。默认值为 1000。
- **`WithSessionTTL(ttl time.Duration)`**：设置会话状态和事件的 TTL。默认值为 0（不过期）。负值等同于 0。
- **`WithAppStateTTL(ttl time.Duration)`**：设置应用级状态的 TTL。默认值为 0（不过期）。
- **`WithUserStateTTL(ttl time.Duration)`**：设置用户级状态的 TTL。默认值为 0（不过期）。
- **`WithKeyPrefix(prefix string)`**：设置 Redis key 前缀。所有 key 将以 `prefix:` 开头。默认无前缀。适用于多应用共享同一 Redis 实例的场景。
- **`WithCompatMode(mode CompatMode)`**：设置存储兼容模式。可选值：`CompatModeNone`、`CompatModeLegacy`（默认）、`CompatModeTransition`。详见下方[存储方式与版本迁移](#存储方式与版本迁移)。

**异步持久化配置：**

- **`WithEnableAsyncPersist(enable bool)`**：启用异步持久化。默认值为 `false`。启用后，`AppendEvent` 和 `AppendTrackEvent` 会将事件写入内部 channel，由后台 worker 异步写入 Redis，降低请求延迟。
- **`WithAsyncPersisterNum(num int)`**：异步持久化 worker 数量。默认值为 10。每个 worker 同时处理 Event 和 TrackEvent 各一个 channel，channel 缓冲区大小为 100。

**摘要配置：**

- **`WithSummarizer(s summary.SessionSummarizer)`**：注入会话摘要器。未设置时摘要相关操作为空操作。
- **`WithAsyncSummaryNum(num int)`**：设置摘要处理 worker 数量。默认值为 3。
- **`WithSummaryQueueSize(size int)`**：设置摘要任务队列大小。默认值为 100。
- **`WithSummaryJobTimeout(timeout time.Duration)`**：设置单个摘要任务超时时间。默认值为 60 秒。

**链路追踪配置：**

- **`WithEnableTracing(enable bool)`**：启用 OpenTelemetry 链路追踪。默认值为 `false`。启用后，`CreateSession`、`GetSession`、`AppendEvent`、`DeleteSession`、`AppendTrackEvent`、`CreateSessionSummary`、`GetSessionSummaryText` 等操作会自动创建 span。

!!! note "关于 Root Span"
    Session 的操作由 Runner 执行，发生在 Agent 的 `Run()` 调用前后。而 Agent 的 root span 是在 `agent.Run()` 内部创建的，Session span 不会自动挂载到 Agent span 下。因此，如果需要在 Langfuse 等可观测平台中看到完整的 Session span 链路，需要在调用 `runner.Run()` 之前手动创建一个 root span：

    ```go
    import atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"

    // Create a root span before runner.Run(), so that session spans
    // (create_session, get_session, append_event, etc.) become children
    // of this root span via context propagation.
    ctx, span := atrace.Tracer.Start(ctx, "my_request")
    defer span.End()

    eventChan, err := r.Run(ctx, userID, sessionID, message)
    ```

**Hook 配置：**

- **`WithAppendEventHook(hooks ...session.AppendEventHook)`**：添加 `AppendEvent` 钩子。
- **`WithGetSessionHook(hooks ...session.GetSessionHook)`**：添加 `GetSession` 钩子。
- Hook 机制是所有 Session 后端的通用能力，详见[高级用法 - Hook 能力](#hook-能力appendget)。

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
    redis.WithSummaryJobTimeout(120*time.Second),
)
```

### 异步持久化

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

!!! warning "注意"
异步持久化模式下，如果服务进程异常退出，channel 中尚未消费的事件可能会丢失。请根据业务对数据一致性的要求评估是否启用。

### 存储方式与版本迁移

Redis Session 存储有两种数据存储引擎，内部通过 `ServiceMeta` 中的 `storage_type` 标记每个 session 所属的存储版本，实现操作路由：


| 存储方式    | 版本             | Hash Tag    | 特点                                                                                                                                            |
| ------------- | ------------------ | ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| **ZSet**    | 旧版（legacy）   | `{appName}` | 所有用户数据集中在同一 Cluster slot，大规模下有热点风险。数据结构简单，Event 直接存储完整 JSON 在 SortedSet 中。                                |
| **HashIdx** | **新版（默认）** | `{userID}`  | 按用户散列，消除热点；数据与索引分离（Hash 存数据 + ZSet 存索引），ZSet 中只存 eventID 避免内存膨胀；Session 元数据独立存储，支持更灵活的查询。 |

* !!! info "如何区分新旧模式"
    - **HashIdx 是新模式**：Session 相关 key 以 `hashidx:` 为前缀，使用 `{userID}` 作为 hash tag，按用户维度散列到不同的 Redis Cluster slot。
    - **ZSet 是旧模式**：Session 相关 key 使用 `{appName}` 作为 hash tag，同一应用的所有用户数据集中在同一个 slot。
    - **AppState 是例外**：`appstate:{appName}` 在两种模式下格式完全一致（没有 `hashidx:` 前缀），因此 AppState 不受存储版本迁移影响，零迁移成本。
    - 新创建的 session 在 `CompatModeLegacy`（默认）和 `CompatModeNone` 模式下使用 HashIdx 存储。

新版本通过 **CompatMode**（兼容模式）实现从旧存储方式到新存储方式的平滑迁移，无需停服。

#### 三种兼容模式


| 模式 | Session 读行为 | Session 写行为 | UserState 行为 | 适用阶段 |
| --- | --- | --- | --- | --- |
| `CompatModeTransition` | Pipeline 同时检查 HashIdx 和 ZSet 是否存在；若 ZSet 存在则读 ZSet，否则读 HashIdx | 仅 ZSet | 写：双写（HashIdx + ZSet）；读：HashIdx 优先，为空回退 ZSet | 滚动升级中，新旧版本实例混合部署 |
| `CompatModeLegacy` **（默认）** | Pipeline 同时检查 HashIdx 和 ZSet 是否存在；若 ZSet 存在则读 ZSet，否则读 HashIdx | 仅 HashIdx | 写：仅 HashIdx；读：HashIdx 优先，为空回退 ZSet | 所有实例已升级，但旧 ZSet 数据尚未过期 |
| `CompatModeNone` | 仅 HashIdx（不检查 ZSet） | 仅 HashIdx | 写：仅 HashIdx；读：仅 HashIdx | ZSet 数据已全部过期，纯新存储模式 |

#### 迁移步骤

```
Phase 1                      Phase 2                      Phase 3
CompatModeTransition         CompatModeLegacy (default)   CompatModeNone
┌─────────────────────┐      ┌─────────────────────┐      ┌────────────────────┐
│ Mixed old/new nodes │  →   │ All nodes upgraded  │  →   │ ZSet data expired  │
│ New nodes write ZSet│      │ New sessions: HIdx  │      │ Pure HashIdx mode  │
│ Same as old nodes   │      │ Old sessions: fbk   │      │ No ZSet overhead   │
└─────────────────────┘      └─────────────────────┘      └────────────────────┘
```

**阶段 1：滚动升级（新旧实例共存）**

使用 `CompatModeTransition`，新实例的读写行为与旧实例完全一致（Session 创建走 ZSet，UserState 双写），保证混合部署下数据完全兼容。

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithCompatMode(redis.CompatModeTransition),
)
```

!!! tip "何时需要阶段 1"
    只有在**灰度发布或新旧版本混合部署**场景下才需要使用 `CompatModeTransition`。如果能够一次性将所有实例升级到新版本（例如全量发布），可以跳过阶段 1，直接使用默认的 `CompatModeLegacy` 即可。

**UserState 注意事项：** 新旧存储方式的 UserState 使用了**不同的 Redis Key**（旧：`userstate:{appName}:{userID}`，新：`hashidx:userstate:appName:{userID}`）。升级到新版本后，通过 HashIdx 创建的新 session 在合并 UserState 时**只会读取新 key**，无法读到旧 key 中的数据。

- 在 `CompatModeTransition` 模式下，`UpdateUserState` 会**同时写入新旧两种 key**，确保新旧实例都能读取。因此**建议在 Transition 阶段通过 `UpdateUserState` 重新写入一次 UserState**，将数据同步到新 key 中。
- 直接调用 `ListUserStates` API 在 Transition 和 Legacy 模式下会先尝试新 key，为空时自动回退到旧 key。但 `CreateSession`/`GetSession` 内部合并 UserState 时不经过此回退逻辑，仅读取新 key。
- **AppState 不受影响**——两种存储方式的 `appstate:{appName}` Key 格式完全一致，无需额外处理。

**阶段 2：全部升级完成**

所有实例均已升级后，切换到 `CompatModeLegacy`（默认值，不需要显式设置）。此后新创建的 session 使用 HashIdx 存储方式，已有的 ZSet 数据仍可通过回退读取访问。

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    // CompatModeLegacy 是默认值，可省略
    // redis.WithCompatMode(redis.CompatModeLegacy),
)
```

!!! warning "注意"
    此处的兼容问题仅发生在**多节点部署且同一用户的请求可能被分发到新旧版本 Session Service 实例**的场景下。如果是单节点部署，或者能通过路由策略保证同一用户始终命中同一版本实例，则无需关注此限制。

**阶段 3：清理完成**

等待 ZSet 数据根据 TTL 自然过期后（如果未设置 TTL，需手动清理），切换到 `CompatModeNone`，完全移除 ZSet 兼容层，获得最佳性能。

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithCompatMode(redis.CompatModeNone),
)
```

#### 新部署（无历史数据）

如果是全新部署，没有旧版本数据，建议使用 `CompatModeNone`，跳过不必要的 ZSet 兼容逻辑：

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithCompatMode(redis.CompatModeNone),
)
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
- **`WithSummaryJobTimeout(timeout time.Duration)`**：设置单个摘要任务超时时间。默认值为 60 秒。

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


完整的表定义请参考 [session/postgres/schema.sql](https://github.com/trpc-group/trpc-agent-go/blob/main/session/postgres/schema.sql)

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
- **`WithSummaryJobTimeout(timeout time.Duration)`**：设置单个摘要任务超时时间。默认值为 60 秒。

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
// - 默认 10 个异步持久化 worker（可通过 WithAsyncPersisterNum 调整）

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


完整的表定义请参考 [session/mysql/schema.sql](https://github.com/trpc-group/trpc-agent-go/blob/main/session/mysql/schema.sql)

### 版本升级

#### 旧版本数据迁移

如果您的数据库是使用旧版本创建的，需要执行以下迁移步骤。

**影响版本**：v1.2.0 之前的版本  
**修复版本**：v1.2.0 及之后

**问题背景**：早期版本的 `session_summaries` 表存在索引设计问题：

- 最初版本使用包含 `deleted_at` 列的唯一索引，但 MySQL 中 `NULL != NULL`，导致多条 `deleted_at = NULL` 的记录无法触发唯一约束
- 后续版本改为普通 lookup 索引（非唯一），同样无法防止重复数据

这两种情况都可能导致重复数据产生。

**旧版索引**（以下两种之一）：

- `idx_*_session_summaries_unique_active(app_name, user_id, session_id, filter_key, deleted_at)` — 唯一索引但包含 deleted_at
- `idx_*_session_summaries_lookup(app_name, user_id, session_id, deleted_at)` — 普通索引

**新版索引**：`idx_*_session_summaries_unique_active(app_name(191), user_id(191), session_id(191), filter_key(191))` — 唯一索引，不包含 deleted_at（使用前缀索引以避免 Error 1071）。

**迁移步骤**：

```sql
-- ============================================================================
-- 迁移脚本：修复 session_summaries 唯一索引问题
-- 执行前请备份数据！
-- ============================================================================

-- Step 1: 查看当前索引，确认旧索引名称
SHOW INDEX FROM session_summaries;

-- Step 2: 清理重复数据（保留最新记录）
-- 如果存在多条 deleted_at = NULL 的重复记录，保留 id 最大的那条。
DELETE t1 FROM session_summaries t1
INNER JOIN session_summaries t2
WHERE t1.app_name = t2.app_name
  AND t1.user_id = t2.user_id
  AND t1.session_id = t2.session_id
  AND t1.filter_key = t2.filter_key
  AND t1.deleted_at IS NULL
  AND t2.deleted_at IS NULL
  AND t1.id < t2.id;

-- Step 3: 硬删除软删除记录（summary 数据可再生，无需保留）
-- 如果需要保留软删除记录，可跳过此步骤，但需要在 Step 5 之前手动处理冲突。
DELETE FROM session_summaries WHERE deleted_at IS NOT NULL;

-- Step 4: 删除旧索引（根据 Step 1 的结果选择正确的索引名）
-- 注意：索引名称可能带有表前缀，请根据实际情况调整。
-- 如果是 lookup 索引：
DROP INDEX idx_session_summaries_lookup ON session_summaries;
-- 如果是旧的 unique_active 索引（包含 deleted_at）：
-- DROP INDEX idx_session_summaries_unique_active ON session_summaries;

-- Step 5: 创建新的唯一索引（不包含 deleted_at）
-- 注意：索引名称可能带有表前缀，请根据实际情况调整。
CREATE UNIQUE INDEX idx_session_summaries_unique_active 
ON session_summaries(app_name(191), user_id(191), session_id(191), filter_key(191));

-- Step 6: 验证迁移结果
SELECT COUNT(*) as duplicate_count FROM (
    SELECT app_name, user_id, session_id, filter_key, COUNT(*) as cnt
    FROM session_summaries
    WHERE deleted_at IS NULL
    GROUP BY app_name, user_id, session_id, filter_key
    HAVING cnt > 1
) t;
-- 期望结果：duplicate_count = 0

-- Step 7: 验证索引是否创建成功
SHOW INDEX FROM session_summaries WHERE Key_name = 'idx_session_summaries_unique_active';
-- 期望结果：显示新创建的唯一索引，且不包含 deleted_at 列
```

**注意事项**：

1. 如果使用了 `WithTablePrefix("trpc_")` 配置，表名和索引名会带有前缀：
   - 表名：`trpc_session_summaries`
   - 旧索引名：`idx_trpc_session_summaries_lookup` 或 `idx_trpc_session_summaries_unique_active`
   - 新索引名：`idx_trpc_session_summaries_unique_active`
   - 请根据实际配置调整上述 SQL 中的表名和索引名。

2. 新索引不包含 `deleted_at` 列，这意味着软删除的 summary 记录会阻止相同业务键的新记录插入。由于 summary 数据可再生，迁移时建议硬删除软删除记录（Step 3）。如果跳过此步骤，需手动处理冲突。


## ClickHouse 存储

适用于生产环境和海量数据场景，利用 ClickHouse 强大的写入吞吐量和数据压缩能力。

### 配置选项

**连接配置：**

- **`WithClickHouseDSN(dsn string)`**：ClickHouse DSN 连接字符串（推荐）。
  - 格式：`clickhouse://user:password@host:port/database?dial_timeout=10s`
- **`WithClickHouseInstance(name string)`**：使用预配置的 ClickHouse 实例。
- **`WithExtraOptions(opts ...any)`**：为 ClickHouse 客户端设置额外选项。

**会话配置：**

- **`WithSessionEventLimit(limit int)`**：每个会话最大事件数量。默认值为 1000。
- **`WithSessionTTL(ttl time.Duration)`**：会话 TTL。默认值为 0（不过期）。
- **`WithAppStateTTL(ttl time.Duration)`**：应用状态 TTL。默认值为 0（不过期）。
- **`WithUserStateTTL(ttl time.Duration)`**：用户状态 TTL。默认值为 0（不过期）。
- **`WithDeletedRetention(retention time.Duration)`**：软删除数据保留时间。默认值为 0（禁用应用层物理清理）。启用后将通过 `ALTER TABLE DELETE` 定期清理软删除数据，生产环境**不建议开启**，建议优先使用 ClickHouse 表级 TTL。
- **`WithCleanupInterval(interval time.Duration)`**：清理任务间隔。

**异步持久化配置：**

- **`WithEnableAsyncPersist(enable bool)`**：启用异步持久化。默认值为 `false`。
- **`WithAsyncPersisterNum(num int)`**：异步持久化 worker 数量。默认值为 10。
- **`WithBatchSize(size int)`**：批量写入大小。默认值为 100。
- **`WithBatchTimeout(timeout time.Duration)`**：批量写入超时。默认值为 100ms。

**摘要配置：**

- **`WithSummarizer(s summary.SessionSummarizer)`**：注入会话摘要器。
- **`WithAsyncSummaryNum(num int)`**：摘要处理 worker 数量。默认值为 3。
- **`WithSummaryQueueSize(size int)`**：摘要任务队列大小。默认值为 100。
- **`WithSummaryJobTimeout(timeout time.Duration)`**：单个摘要任务超时时间。

**Schema 配置：**

- **`WithTablePrefix(prefix string)`**：表名前缀。
- **`WithSkipDBInit(skip bool)`**：跳过自动建表。

**Hook 配置：**

- **`WithAppendEventHook(hooks ...session.AppendEventHook)`**：添加事件写入 Hook。
- **`WithGetSessionHook(hooks ...session.GetSessionHook)`**：添加会话读取 Hook。

### 基础配置示例

```go
import "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"

// 默认配置（最简）
sessionService, err := clickhouse.NewService(
    clickhouse.WithClickHouseDSN("clickhouse://default:password@localhost:9000/default"),
)
```

### 配置复用

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
    sessionch "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
)

// 注册 ClickHouse 实例
clickhouse.RegisterClickHouseInstance("my-clickhouse",
    clickhouse.WithClientBuilderDSN("clickhouse://localhost:9000/default"),
)

// 在会话服务中使用
sessionService, err := sessionch.NewService(
    sessionch.WithClickHouseInstance("my-clickhouse"),
)
```

### 存储结构

ClickHouse 实现使用了 `ReplacingMergeTree` 引擎来处理数据更新和去重。

**关键特性：**

1.  **ReplacingMergeTree**：利用 `updated_at` 字段，ClickHouse 会在后台自动合并相同主键的记录，保留最新版本。
2.  **FINAL 查询**：所有读取操作都使用 `FINAL` 关键字（如 `SELECT ... FINAL`），确保在查询时合并所有数据部分，保证读取一致性。
3.  **Soft Delete**：删除操作通过插入一条带有 `deleted_at` 时间戳的新记录实现。查询时过滤 `deleted_at IS NULL`。

```sql
-- 会话状态表
CREATE TABLE IF NOT EXISTS session_states (
    app_name    String,
    user_id     String,
    session_id  String,
    state       JSON COMMENT 'Session state in JSON format',
    extra_data  JSON COMMENT 'Additional metadata',
    created_at  DateTime64(6),
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time (application-level)',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
-- CRITICAL: Removed deleted_at from ORDER BY to allow ReplacingMergeTree to collapse deleted records
ORDER BY (app_name, user_id, session_id)
SETTINGS allow_nullable_key = 1
COMMENT 'Session states table';

-- 会话事件表
CREATE TABLE IF NOT EXISTS session_events (
    app_name    String,
    user_id     String,
    session_id  String,
    event_id    String,
    event       JSON COMMENT 'Event data in JSON format',
    extra_data  JSON COMMENT 'Additional metadata',
    created_at  DateTime64(6),
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Reserved for future use',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
-- CRITICAL: Removed deleted_at from ORDER BY to allow ReplacingMergeTree to collapse deleted records
ORDER BY (app_name, user_id, session_id, event_id)
SETTINGS allow_nullable_key = 1
COMMENT 'Session events table';

-- 会话摘要表
CREATE TABLE IF NOT EXISTS session_summaries (
    app_name    String,
    user_id     String,
    session_id  String,
    filter_key  String COMMENT 'Filter key for multiple summaries per session',
    summary     JSON COMMENT 'Summary data in JSON format',
    created_at  DateTime64(6),
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Reserved for future use',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
-- CRITICAL: Removed deleted_at from ORDER BY to allow ReplacingMergeTree to collapse deleted records
ORDER BY (app_name, user_id, session_id, filter_key)
SETTINGS allow_nullable_key = 1
COMMENT 'Session summaries table';

-- 应用状态表
CREATE TABLE IF NOT EXISTS app_states (
    app_name    String,
    key         String COMMENT 'State key',
    value       String COMMENT 'State value',
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time (application-level)',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY app_name
-- CRITICAL: Removed deleted_at from ORDER BY to allow ReplacingMergeTree to collapse deleted records
ORDER BY (app_name, key)
SETTINGS allow_nullable_key = 1
COMMENT 'Application states table';

-- 用户状态表
CREATE TABLE IF NOT EXISTS user_states (
    app_name    String,
    user_id     String,
    key         String COMMENT 'State key',
    value       String COMMENT 'State value',
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time (application-level)',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY (app_name, cityHash64(user_id) % 64)
-- CRITICAL: Removed deleted_at from ORDER BY to allow ReplacingMergeTree to collapse deleted records
ORDER BY (app_name, user_id, key)
SETTINGS allow_nullable_key = 1
COMMENT 'User states table';
```

## 高级用法

### Hook 能力（Append/Get）

- **AppendEventHook**：事件写入前的拦截/修改/终止。可用于内容安全、审计打标（如写入 `violation=<word>`），或直接阻断存储。关于 filterKey 的赋值请见下文“会话摘要 / FilterKey 与 AppendEventHook”。
- **GetSessionHook**：会话读取后的拦截/修改/过滤。可用来剔除带特定标签的事件，或动态补充返回的 Session 状态。
- **责任链执行**：Hook 通过 `next()` 形成链式调用，可提前返回以短路后续逻辑，错误会向上传递。
- **跨后端一致**：内存、SQLite、Redis、MySQL、PostgreSQL 实现已统一接入 Hook，构造服务时注入 Hook 切片即可。
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
    inmemory.WithSummaryJobTimeout(60*time.Second), // 单个任务超时 60 秒
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

// ClickHouse 存储
sessionService, err := clickhouse.NewService(
    clickhouse.WithClickHouseDSN("clickhouse://default:password@localhost:9000/default"),
    clickhouse.WithSummarizer(summarizer),
    clickhouse.WithAsyncSummaryNum(2),
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

- 会话摘要会合并到已有系统消息中；如果没有系统消息，则前置添加一条新的系统消息
- 包含摘要时间点之后的**所有增量事件**（不截断）
- 保证完整上下文：浓缩历史 + 完整新对话
- **`WithMaxHistoryRuns` 参数被忽略**

**可选：Prompt 侧上下文压缩**

当开启 `WithEnableContextCompaction(true)` 时，框架会在真正调用模型前增加一层轻量压缩：

- 只压缩旧 request 中过长的 `tool result`
- 只替换正文，仍保留 `ToolID` 和 `ToolName`
- 当前 request 永远不压
- 最近 `ContextCompactionKeepRecentRequests` 个已完成 request 会完整保留
- 如果同时开启了 `WithAddSessionSummary(true)`，并且压完后请求仍接近 context window，会在 LLM 调用前同步执行一次 `CreateSessionSummary(...)` 并重建 request
- 模型层的 token tailoring 仍然作为最后兜底

```go
agent := llmagent.New(
    "my-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithAddSessionSummary(true),
    llmagent.WithEnableContextCompaction(true),
    llmagent.WithContextCompactionThresholdRatio(0.7),
    llmagent.WithContextCompactionToolResultMaxTokens(1024),
    llmagent.WithContextCompactionKeepRecentRequests(1),
)
```

**上下文结构：**

```text
┌─────────────────────────────────────────┐
│ System Prompt                           │ ← 若已存在系统消息，则与摘要合并；
│ (merged with Session Summary)           │    否则前置插入新的系统消息
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
- 如果开启 `WithEnableContextCompaction(true)`，保留下来的旧 request 中超长 `tool result` 仍可在 request projection 阶段被压缩
- 这个模式下不会触发 pre-LLM 的同步摘要重试

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

如果你的长会话里经常出现搜索结果、日志、代码扫描输出这类长 `tool result`，建议开启 `EnableContextCompaction=true`。如果你还希望在接近 context window 时多一次同步摘要兜底，再配合 `AddSessionSummary=true` 一起使用。

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

- **`WithContextThreshold(opts ...ContextThresholdOption)`**：零配置触发器，在每次评估时动态感知当前模型的 context window。根据 context window 的比例（默认 50%）自动计算 token 阈值，当用户切换模型时自动适配，无需重建 Summarizer。这是大多数场景下的推荐选项，类似 Codex CLI 和 Claude Code 的 auto-compact 行为。示例：`WithContextThreshold()` 零配置使用，或 `WithContextThreshold(summary.WithContextThresholdRatio(0.6))` 自定义比例。
- **`WithEventThreshold(eventCount int)`**：当自上次摘要后的事件数量超过阈值时触发摘要。示例：`WithEventThreshold(20)` 在自上次摘要后新增 20 个事件后触发。
- **`WithTokenThreshold(tokenCount int)`**：当自上次摘要后的 token 数量超过阈值时触发摘要。示例：`WithTokenThreshold(4000)` 在自上次摘要后新增 4000 个 token 后触发。
- **`WithTimeThreshold(interval time.Duration)`**：当自上次事件后经过的时间超过间隔时触发摘要。示例：`WithTimeThreshold(5*time.Minute)` 在 5 分钟无活动后触发。

!!! note "Context Window 注册"
    `WithContextThreshold` 和 Token Tailoring 都依赖框架内置的模型 context window 注册表。注册表已包含大量常见模型（OpenAI、Anthropic、Google、DeepSeek、Qwen 等），但不一定覆盖所有模型——特别是私有部署、微调变体或较新发布的模型。如果你的模型未被识别（context window 解析为 0 或回退到默认值），请在启动时手动注册：

    ```go
    import "trpc.group/trpc-go/trpc-agent-go/model"

    func init() {
        // 注册单个模型
        model.RegisterModelContextWindow("my-custom-model", 32768)

        // 或批量注册多个模型
        model.RegisterModelContextWindows(map[string]int{
            "my-custom-model-32k":  32768,
            "my-custom-model-128k": 131072,
        })
    }
    ```

    模型名匹配不区分大小写，注册表也支持前缀匹配（例如注册 `"my-model"` 会匹配 `"my-model-v2"`）。

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

#### Token 计数器配置（Token Counter Configuration）

默认情况下，`CheckTokenThreshold` 使用内置的 `SimpleTokenCounter` 基于文本长度估算 token 数量。如果您需要自定义 token 计数行为（例如，为特定模型使用更精确的 tokenizer），可以使用 `summary.SetTokenCounter` 设置全局 token 计数器：

```go
import (
    "context"
    "fmt"
    "unicode/utf8"

    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// 设置自定义 token 计数器（影响该进程中的所有 CheckTokenThreshold 评估）
summary.SetTokenCounter(model.NewSimpleTokenCounter())

// 或者使用自定义实现
type MyCustomCounter struct{}

func (c *MyCustomCounter) CountTokens(ctx context.Context, message model.Message) (int, error) {
    _ = ctx
    // TODO: Replace this with your real tokenizer implementation.
    return utf8.RuneCountInString(message.Content), nil
}

func (c *MyCustomCounter) CountTokensRange(ctx context.Context, messages []model.Message, start, end int) (int, error) {
    if start < 0 || end > len(messages) || start >= end {
        return 0, fmt.Errorf("invalid range: start=%d, end=%d, len=%d",
            start, end, len(messages))
    }

    total := 0
    for i := start; i < end; i++ {
        tokens, err := c.CountTokens(ctx, messages[i])
        if err != nil {
            return 0, err
        }
        total += tokens
    }
    return total, nil
}

summary.SetTokenCounter(&MyCustomCounter{})

// 创建带 token 阈值检查器的摘要器
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.CheckTokenThreshold(4000),  // 将使用您的自定义计数器
)
```

**重要说明：**

- **全局影响**：`SetTokenCounter` 会影响当前进程中所有的 `CheckTokenThreshold` 评估。建议在应用初始化时一次性设置。
- **默认计数器**：如果不设置，将使用默认配置的 `SimpleTokenCounter`（约每 token 对应 4 个字符）。
- **使用场景**：
  - 需要精确估算 token 时使用准确的 tokenizer（如 tiktoken）
  - 针对特定语言的模型进行调整（中文模型的 token 密度可能不同）
  - 集成模型特定的计数 API 以获得更好的准确性

**工具调用格式化：**

默认情况下，摘要器会将工具调用和工具结果包含在发送给 LLM 进行总结的对话文本中。默认格式为：

- 工具调用：`[Called tool: toolName with args: {"arg": "value"}]`
- 工具结果：`[toolName returned: result content]`

你可以使用以下选项自定义工具调用和结果的格式化方式：

- **`WithToolCallFormatter(f ToolCallFormatter)`**：自定义工具调用在摘要输入中的格式。格式化器接收 `model.ToolCall` 并返回格式化字符串。返回空字符串可排除该工具调用。
- **`WithToolResultFormatter(f ToolResultFormatter)`**：自定义工具结果在摘要输入中的格式。格式化器接收包含工具结果的 `model.Message` 并返回格式化字符串。返回空字符串可排除该结果。

**自定义工具格式化器示例：**

```go
// 截断过长的工具参数
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithToolCallFormatter(func(tc model.ToolCall) string {
        name := tc.Function.Name
        if name == "" {
            return ""
        }
        args := string(tc.Function.Arguments)
        const maxLen = 100
        if len(args) > maxLen {
            args = args[:maxLen] + "...(已截断)"
        }
        return fmt.Sprintf("[工具: %s, 参数: %s]", name, args)
    }),
    summary.WithEventThreshold(20),
)

// 从摘要中排除工具结果
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithToolResultFormatter(func(msg model.Message) string {
        return "" // 返回空字符串以排除工具结果。
    }),
    summary.WithEventThreshold(20),
)

// 仅包含工具名称，排除参数
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithToolCallFormatter(func(tc model.ToolCall) string {
        if tc.Function.Name == "" {
            return ""
        }
        return fmt.Sprintf("[使用工具: %s]", tc.Function.Name)
    }),
    summary.WithEventThreshold(20),
)
```

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
- **`WithAsyncSummaryNum(num int)`**：设置用于摘要处理的异步 worker goroutine 数量。默认为 3。更多 worker 允许更高并发但消耗更多资源。
- **`WithSummaryQueueSize(size int)`**：设置摘要任务队列的大小。默认为 100。更大的队列允许更多待处理任务但消耗更多内存。
- **`WithSummaryJobTimeout(timeout time.Duration)`**：设置处理单个摘要任务的超时时间。默认为 60 秒。

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
        inmemory.WithSummaryJobTimeout(60*time.Second),
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
