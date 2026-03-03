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

#### 同步摘要刷新（SyncSummaryIntraRun）

默认情况下，会话摘要由后台工作线程在工具调用结果事件后异步生成。这意味着 LLM 可用的摘要可能滞后于当前对话状态。如果需要在同一轮次内的每次 LLM 调用前确保摘要是最新的，可以启用同步摘要刷新。

**适用场景：**

- 长工具调用链（ReAct 循环），中间上下文很重要
- 需要 LLM 始终看到最新摘要的场景
- 需要最小化摘要延迟的场景

**配置示例：**

```go
agent := llmagent.New(
    "my-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithAddSessionSummary(true),
    llmagent.WithSyncSummaryIntraRun(true), // 启用同步摘要刷新
)
```

**工作机制：**

启用 `SyncSummaryIntraRun` 后：

1. **首次 LLM 调用**：从会话加载摘要（可能为空或过期）
2. **迭代之间**：在每次后续 LLM 调用前同步刷新摘要
3. **最终响应**：仍会触发异步摘要入队，确保会话摘要完整

框架会自动跳过同一轮次内中间工具结果事件的冗余异步摘要入队，避免重复工作，同时确保最终摘要是完整的。

**行为对比：**

| 模式 | 摘要时机 | 延迟 | 资源消耗 |
|------|---------|------|---------|
| 异步（默认） | 后台工作线程 | 可能滞后 | 每次迭代较低 |
| 同步轮内 | 每次 LLM 调用前 | 始终最新 | 较高（同步） |

**重要注意事项：**

- `SyncSummaryIntraRun` 需要启用 `AddSessionSummary`
- 同步摘要刷新会增加每次 LLM 迭代的延迟
- 对于大多数场景，异步摘要（默认）已足够

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
- **`WithSummaryJobTimeout(timeout time.Duration)`**：设置单个摘要任务超时时间。默认值为 60 秒。
- **`WithKeyPrefix(prefix string)`**：设置 Redis key 前缀。所有 key 将以 `prefix:` 开头。默认无前缀。
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

#### FilterKey、EventFilterKey 与 BranchFilterMode（先搞懂再用）

很多同学第一次接触 FilterKey 会觉得难理解，主要原因是它同时参与两类能力：

1. **会话摘要**：用某个 filterKey 生成/读取摘要（`CreateSessionSummary`、
   `GetSessionSummaryText` + `WithSummaryFilterKey`）。
2. **历史可见性**：构建下一次 Prompt 时，决定“哪些历史事件会被放进上下文”
   （`WithMessageBranchFilterMode`）。

你可以把 `FilterKey` 当成一个**分层路径**（像文件路径一样）：

- `my-app/user-messages`
- `my-app/tool-calls`
- `my-app/auth/role_admin`

`/` 是分隔符，因此这些 key 会形成一棵“树”：

```text
my-app
├── user-messages
├── tool-calls
└── auth
    ├── role_admin
    └── role_viewer
```

这里还有一个容易混淆的概念：`EventFilterKey`。

- `Event.FilterKey`：每条事件自带的 key（你可以在 `AppendEventHook` 里设置）。
- `Invocation.eventFilterKey`：本次运行的“视图 key”，用于筛选历史事件；可通过
  `agent.WithEventFilterKey(...)` 在 `runner.Run(...)` 时设置。

当框架把历史事件注入到 Prompt 时，会用 `WithMessageBranchFilterMode` 选择匹配
规则：

| 模式 | 直觉解释 | 会包含哪些事件（相对当前 EventFilterKey） |
|------|----------|------------------------------------------|
| `prefix`（默认） | **同一条祖先链都算** | 祖先、自己、子孙 |
| `subtree` | **只看当前子树** | 自己、子孙（不含祖先） |
| `exact` | **必须完全相等** | 仅自己 |
| `all` | **不做隔离** | 全部 |

**注意：** 为了兼容旧行为，当 `EventFilterKey==""` 或 `Event.FilterKey==""` 时，
框架会把它当作“匹配所有”，因此这些模式都会倾向于包含更多历史。

> 小结：FilterKey 不只是“分类标签”，它更像“会话视图/作用域”。想做权限隔离时，
> 一定要同时考虑匹配模式（尤其是 `prefix` vs `subtree`）以及摘要注入行为。

#### 使用 AppendEventHook 设置 FilterKey

推荐使用 `AppendEventHook` 在事件写入前自动设置 `FilterKey`：

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
        // 根据事件作者自动分类
        prefix := "my-app/"  // 推荐：以 appName 作为根前缀
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

**强烈推荐：让 FilterKey 以 `appName/` 开头（或等于 `appName`）。**

**原因：**

- Runner 默认会把本次运行的 `EventFilterKey` 设为 `appName`（除非你显式传入
  `agent.WithEventFilterKey(...)`）。
- 历史注入与摘要都依赖“层级匹配”：如果你的事件 `FilterKey` 不在 `appName` 这棵树
  下，那么在默认配置下它很可能不会进入 Prompt，进而导致：

- LLM 看不到历史对话，可能重复触发工具调用
- 摘要内容不完整，丢失重要上下文

**示例：**

```go
// ✅ 正确：带 appName 前缀
evt.FilterKey = "my-app/user-messages"

// ❌ 错误：没有前缀，事件会被过滤
evt.FilterKey = "user-messages"
```

**技术细节：**

- `prefix` 模式使用 `event.Event.Filter(filterKey)` 做层级匹配：只要两者存在祖先/
  后代关系就算匹配（基于 `/` 分隔符的前缀判断）。
- `subtree` 模式只包含“当前 key 及其子孙”，不包含父级（更适合严格隔离）。

#### 完整示例

参考以下示例查看完整的 FilterKey 使用场景：

- [examples/session/hook](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/hook) - Hook 基础用法
- [examples/summary/filterkey](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary/filterkey) - 按 FilterKey 生成摘要

### 权限变更与历史隔离（避免“旧权限答案复用”）

很多业务会把**权限校验**放在 `BeforeToolCallback` 里：模型要调用工具 → 回调里校验权限 → 通过才执行工具。

这在“工具确实被调用”的情况下是可靠的，但会遇到一个容易被忽略的问题：

- 同一个 session 里，用户问过一次问题（当时有权限），工具返回了结果并写入了历史。
- 后来用户权限被撤销/变更，但仍在同一个 session 里再次问同样的问题。
- 模型可能**直接根据历史消息作答**，不再触发工具调用。
- 这时你的 `BeforeToolCallback` **不会执行**，从而出现“旧权限结果被复用”的风险。

要从根上解决，关键点不是“让模型一定调用工具”，而是：

1. **工具层仍然做权限校验**（防线 1）。
2. **Prompt 构建时不要把旧权限下的敏感历史放进上下文**（防线 2）。

下面给出两种常见做法。

#### 方案 A：权限变更时切换会话（最简单）

当检测到权限发生变化（角色变了、权限版本号变了等）时，直接使用新的 `sessionID`，
或在该次请求中禁用历史注入（只保留本轮消息和本轮工具回合上下文）。

优点：简单直接，最不容易出错。  
缺点：会丢失旧会话上下文（这是安全设计下常见的权衡）。

#### 方案 B：用 FilterKey 给“同一 session”做权限视图隔离（推荐）

核心思路：把“权限快照”当成一个**会话视图（view）**，写入 FilterKey。

- 权限快照未变化：继续使用相同的 FilterKey，历史可复用。
- 权限快照变化：切换到新的 FilterKey，旧视图下的历史不会进入上下文。

**第 1 步：Run 时传入 EventFilterKey**

Runner 支持在每次 `Run` 时指定本次请求使用的 FilterKey 前缀：

```go
appName := "my-app"
viewKey := "auth/role_admin" // 示例：用角色/权限版本构造
filterKey := appName + "/" + viewKey

events, err := app.Run(
    ctx,
    userID,
    sessionID,
    msg,
    agent.WithEventFilterKey(filterKey),
)
_ = events
_ = err
```

你只需要保证：当权限发生变化时，`viewKey` 也跟着变化即可。

**第 2 步：使用 Subtree 分支过滤模式（避免继承父级 FilterKey）**

如果你的历史里曾经写入过更“粗”的 FilterKey（比如仅 `my-app`），
那么在默认的 Prefix 模式下，`my-app` 可能会被视为 `my-app/auth/...` 的父级而被包含进来。

此时可以把 Agent 的消息分支过滤模式设置为 `subtree`：

```go
ag := llmagent.New(
    "assistant",
    llmagent.WithMessageBranchFilterMode(llmagent.BranchFilterModeSubtree),
)
_ = ag
```

`subtree` 的语义是：只包含“当前 FilterKey 本身及其子节点”的事件，不包含父级。
这对“权限视图隔离”非常重要。

**补充：Session Summary 与 subtree**

如果你启用了 `WithAddSessionSummary(true)`，框架会把会话摘要注入到 system 消息。
需要注意：当前摘要生成按 `event.Filter` 的层级匹配规则过滤事件，
这会把**父级 FilterKey** 的事件也算作“匹配”（例如 `my-app` 会匹配
`my-app/auth/...`）。因此在“权限视图隔离”场景下，如果历史里存在父级事件，
摘要可能仍会把父级内容带入上下文，从而削弱隔离效果。

对需要严格隔离的场景，建议：

- 保持 `AddSessionSummary=false`（默认）；
- 或在权限变更时切换 `sessionID`（方案 A）；
- 或确保不会写入父级 FilterKey 的敏感事件。

**注意：这不是替代权限校验。**  
你仍然需要在工具执行层做真实的鉴权（例如在 `BeforeToolCallback` 或工具实现内部），
FilterKey 视图隔离只是为了避免模型在 Prompt 里看到不该看到的历史。

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
