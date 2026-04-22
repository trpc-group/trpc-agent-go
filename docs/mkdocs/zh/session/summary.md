# 会话摘要（Summary）

## 概述

随着对话持续增长，维护完整的事件历史可能会占用大量内存，并可能超出 LLM 的上下文窗口限制。会话摘要功能使用 LLM 自动将历史对话压缩为简洁的摘要，在保留重要上下文的同时显著降低内存占用和 token 消耗。

## 核心特性

- **自动触发**：在执行摘要检查时，根据事件数量、token 数量或时间阈值自动生成摘要
- **增量处理**：只处理自上次摘要以来的新事件，避免重复计算
- **LLM 驱动**：使用任何配置的 LLM 模型生成高质量、上下文感知的摘要
- **非破坏性**：原始事件完整保留，摘要单独存储
- **异步处理**：后台异步执行，不阻塞对话流程
- **灵活配置**：支持自定义触发条件、提示词和字数限制

## 基础配置

### 步骤 1：创建摘要器

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
        summary.CheckTimeThreshold(5*time.Minute), // 在摘要检查时判断；比较被检查 session 的最后一个事件（在增量摘要路径里通常就是最近一个待摘要事件）
    ),
    summary.WithMaxSummaryWords(200),          // 限制摘要在 200 字以内
)
```

### 步骤 2：配置会话服务

将摘要器集成到会话服务（内存或 Redis）：

```go
import (
    "context"
    "time"
    "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/session/mysql"
    "trpc.group/trpc-go/trpc-agent-go/session/postgres"
    "trpc.group/trpc-go/trpc-agent-go/session/redis"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
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

### 步骤 3：配置 Agent 和 Runner

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

## SessionSummarizer 接口

```go
type SessionSummarizer interface {
    // ShouldSummarize checks if the session should be summarized.
    ShouldSummarize(sess *session.Session) bool

    // Summarize generates a summary without modifying the session events.
    Summarize(ctx context.Context, sess *session.Session) (string, error)

    // SetPrompt updates the summarizer's prompt dynamically.
    SetPrompt(prompt string)

    // SetModel updates the summarizer's model dynamically.
    SetModel(m model.Model)

    // Metadata returns metadata about the summarizer configuration.
    Metadata() map[string]any
}
```

## 上下文感知的摘要检查

已发布的 `SessionSummarizer` 接口保持不变。

如果摘要触发条件依赖请求上下文，可以直接使用 `ContextChecker`
以及带 context 的检查选项：

```go
type asyncSummaryKey struct{}

eventThreshold := summary.CheckEventThreshold(20)

summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithChecksAnyContext(
        func(ctx context.Context, sess *session.Session) bool {
            if eventThreshold(sess) {
                return true
            }
            async, _ := ctx.Value(asyncSummaryKey{}).(bool)
            return async
        },
    ),
)
```

框架本身不会为摘要触发方式预留 context key。如果业务需要区分不同的
摘要入口，可以在调用 session API 之前自行往 `ctx` 写入标记，并在
`ContextChecker` 中读取。

## 摘要器选项

### 触发条件

| 选项 | 说明 |
| --- | --- |
| `WithEventThreshold(eventCount int)` | 当自上次摘要后的事件数量超过阈值时触发 |
| `WithTokenThreshold(tokenCount int)` | 当自上次摘要后的 token 数量超过阈值时触发 |
| `WithTimeThreshold(interval time.Duration)` | 在执行摘要检查时，包装 `CheckTimeThreshold`；当被检查 session 的最后一个事件距离当前已超过该间隔时触发 |

### 组合条件

| 选项 | 说明 |
| --- | --- |
| `WithChecksAll(checks ...Checker)` | 要求所有条件都满足（AND 逻辑），使用 `Check*` 函数 |
| `WithChecksAny(checks ...Checker)` | 任何条件满足即触发（OR 逻辑），使用 `Check*` 函数 |
| `WithChecksAllContext(checks ...ContextChecker)` | 要求所有带请求上下文的条件都满足（AND 逻辑） |
| `WithChecksAnyContext(checks ...ContextChecker)` | 任一带请求上下文的条件满足即触发（OR 逻辑） |

`ContextChecker` 的签名为 `(ctx context.Context, sess *session.Session)`。

**注意**：在 `WithChecksAll` 和 `WithChecksAny` 中使用 `Check*` 函数（如 `CheckEventThreshold`），而不是 `With*` 函数。

```go
// AND 逻辑：所有条件都满足才触发
summary.WithChecksAll(
    summary.CheckEventThreshold(10),
    summary.CheckTokenThreshold(2000),
)

// OR 逻辑：任一条件满足即触发
summary.WithChecksAny(
    summary.CheckEventThreshold(50),
    summary.CheckTimeThreshold(10*time.Minute),
)
```

### 摘要生成

| 选项 | 说明 |
| --- | --- |
| `WithMaxSummaryWords(maxWords int)` | 限制摘要的最大字数，包含在提示词中指导模型生成 |
| `WithPrompt(prompt string)` | 自定义摘要提示词，必须包含 `{conversation_text}` 占位符 |
| `WithSystemPrompt(prompt string)` | 为摘要额外添加独立的 system message 指令；不能包含 `{conversation_text}` |
| `WithSkipRecent(skipFunc SkipRecentFunc)` | 自定义函数跳过最近事件 |

### Hook 选项

| 选项 | 说明 |
| --- | --- |
| `WithPreSummaryHook(h PreSummaryHook)` | 摘要前的 Hook，可修改输入文本 |
| `WithPostSummaryHook(h PostSummaryHook)` | 摘要后的 Hook，可修改输出摘要 |
| `WithSummaryHookAbortOnError(abort bool)` | Hook 报错时是否中断，默认 `false`（忽略错误） |

### 工具调用格式化

默认情况下，摘要器会将工具调用和工具结果包含在发送给 LLM 进行总结的对话文本中。默认格式为：

- 工具调用：`[Called tool: toolName with args: {"arg": "value"}]`
- 工具结果：`[toolName returned: result content]`

| 选项 | 说明 |
| --- | --- |
| `WithToolCallFormatter(f ToolCallFormatter)` | 自定义工具调用在摘要输入中的格式。返回空字符串可排除该工具调用 |
| `WithToolResultFormatter(f ToolResultFormatter)` | 自定义工具结果在摘要输入中的格式。返回空字符串可排除该结果 |

```go
// Truncate long tool arguments
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
            args = args[:maxLen] + "...(truncated)"
        }
        return fmt.Sprintf("[Tool: %s, Args: %s]", name, args)
    }),
    summary.WithEventThreshold(20),
)

// Exclude tool results from summary
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithToolResultFormatter(func(msg model.Message) string {
        return ""
    }),
    summary.WithEventThreshold(20),
)

// Include only tool name, exclude arguments
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithToolCallFormatter(func(tc model.ToolCall) string {
        if tc.Function.Name == "" {
            return ""
        }
        return fmt.Sprintf("[Used tool: %s]", tc.Function.Name)
    }),
    summary.WithEventThreshold(20),
)
```

### 模型回调（Before/After Model）

`summarizer` 在调用底层 `model.GenerateContent` 前后支持模型回调，可用于修改请求、短路返回自定义响应、或在摘要请求上做埋点。

| 选项 | 说明 |
| --- | --- |
| `WithModelCallbacks(callbacks *model.Callbacks)` | 为摘要器的底层模型调用注册 Before/After 回调 |

```go
callbacks := model.NewCallbacks().
    RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
        // Modify args.Request, or return CustomResponse to skip the real model call
        return nil, nil
    }).
    RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
        // Override model output via CustomResponse
        return nil, nil
    })

summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithModelCallbacks(callbacks),
)
```

## Checker 函数

Checker 是用于判断是否需要触发摘要的函数类型：

```go
type Checker func(sess *session.Session) bool
```

### 内置 Checker

| Checker | 说明 |
| --- | --- |
| `CheckEventThreshold(eventCount int)` | 当自上次摘要以来的增量事件数大于阈值时返回 true |
| `CheckTimeThreshold(interval time.Duration)` | 当被检查 session 的最后一个事件距离当前已超过该间隔时返回 true |
| `CheckTokenThreshold(tokenCount int)` | 当自上次摘要以来的增量事件提取的对话文本估算 token 数大于阈值时返回 true（通过 `TokenCounter` 估算，而非 `event.Response.Usage.TotalTokens`） |
| `ChecksAll(checks []Checker)` | 组合多个 Checker，所有都返回 true 时才返回 true（AND） |
| `ChecksAny(checks []Checker)` | 组合多个 Checker，任一返回 true 时返回 true（OR） |

## 自定义提示词

```go
customPrompt := `Analyze the following conversation and provide a concise summary,
focusing on key decisions, action items, and important context.
Keep it within {max_summary_words} words.

<conversation>
{conversation_text}
</conversation>

Summary:`

summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithPrompt(customPrompt),
    summary.WithMaxSummaryWords(100),
    summary.WithEventThreshold(15),
)
```

**必需占位符**：

- `{conversation_text}`：必须包含，会被对话内容替换
- `{max_summary_words}`：当 `maxSummaryWords > 0` 时，必须包含在 `WithPrompt(...)` 或 `WithSystemPrompt(...)` 其中之一

如果希望把摘要指令放到独立的 system message，可以组合使用
`WithSystemPrompt` 和一个更轻量的 user prompt：

```go
systemPrompt := `请忠实总结这段对话。
重点关注关键决策和待办事项。
请控制在 {max_summary_words} 字以内。`

userPrompt := `<conversation>
{conversation_text}
</conversation>

摘要：`

summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithSystemPrompt(systemPrompt),
    summary.WithPrompt(userPrompt),
    summary.WithMaxSummaryWords(100),
    summary.WithEventThreshold(15),
)
```

说明：

- `WithPrompt` 仍然渲染到 **user message**
- `WithSystemPrompt` 会渲染到独立的 **system message**
- `WithSystemPrompt` 不能包含 `{conversation_text}`；对话内容必须保留在 user prompt 中

## Token 计数器配置

默认情况下，`CheckTokenThreshold` 使用内置的 `SimpleTokenCounter` 基于文本长度估算 token 数量。如果需要自定义 token 计数行为，可以使用 `summary.SetTokenCounter` 设置全局 token 计数器：

```go
import (
    "context"
    "fmt"
    "unicode/utf8"

    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Use the built-in simple token counter
summary.SetTokenCounter(model.NewSimpleTokenCounter())

// Or use a custom implementation
type MyCustomCounter struct{}

func (c *MyCustomCounter) CountTokens(ctx context.Context, message model.Message) (int, error) {
    _ = ctx
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
```

**注意**：

- **全局影响**：`SetTokenCounter` 会影响当前进程中所有的 `CheckTokenThreshold` 评估，建议在应用初始化时一次性设置
- **默认计数器**：如果不设置，将使用默认的 `SimpleTokenCounter`（约每 token 对应 4 个字符）

## 跳过最近事件

使用 `WithSkipRecent` 可以在摘要时跳过最近的事件：

```go
// 跳过固定数量的事件
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithSkipRecent(func(_ []event.Event) int { return 2 }), // 跳过最后 2 个事件
    summary.WithEventThreshold(10),
)

// 跳过最近 5 分钟的事件（时间窗口）
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

// 只跳过尾部的工具调用消息
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

## 摘要 Hook

### PreSummaryHook

在摘要生成前调用，可以修改输入文本或事件：

```go
type PreSummaryHookContext struct {
    Ctx     context.Context
    Session *session.Session
    Events  []event.Event
    Text    string
}

type PreSummaryHook func(in *PreSummaryHookContext) error
```

### PostSummaryHook

在摘要生成后调用，可以修改输出摘要：

```go
type PostSummaryHookContext struct {
    Ctx     context.Context
    Session *session.Session
    Summary string
}

type PostSummaryHook func(in *PostSummaryHookContext) error
```

### 使用示例

```go
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithPreSummaryHook(func(ctx *summary.PreSummaryHookContext) error {
        // 在摘要生成前修改 ctx.Text 或 ctx.Events
        return nil
    }),
    summary.WithPostSummaryHook(func(ctx *summary.PostSummaryHookContext) error {
        // 在摘要生成后修改 ctx.Summary
        return nil
    }),
    summary.WithSummaryHookAbortOnError(true), // Hook 报错时中断（可选）
)
```

## 摘要触发机制

### 自动触发（推荐）

Runner 在每次对话完成后自动检查触发条件，满足条件时在后台异步生成摘要。

**触发时机**：

- 事件数量超过阈值（`WithEventThreshold`）
- Token 数量超过阈值（`WithTokenThreshold`）
- 在一次摘要检查中，被检查 session 的最后一个事件已超过指定时间；在默认增量摘要路径里，这通常就是最近一个待摘要事件（`WithTimeThreshold`）
- 满足自定义组合条件（`WithChecksAny` / `WithChecksAll`）

`WithTimeThreshold` 不是后台定时器。系统不会在“静默满 5 分钟”的瞬间主动生成摘要；只有在执行摘要检查时才会评估，通常发生在一轮对话结束后，或你手动调用摘要 API 时。它判断的是被检查 session 的最后一个事件；在默认增量摘要路径里，这个 session 只包含待摘要增量，所以 `5*time.Minute` 通常等价于：“到下一次摘要检查时，如果最近一个待摘要事件已经超过 5 分钟，就立即生成摘要。”

### 手动触发

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

| 场景 | 推荐 API | force 参数 |
| --- | --- | --- |
| 正常对话流程 | 自动触发（无需调用） | - |
| 后台批量处理 | `EnqueueSummaryJob` | `false` |
| 用户主动请求 | `EnqueueSummaryJob` | `true` |
| 调试/测试 | `CreateSessionSummary` | `true` |
| 会话结束时 | `EnqueueSummaryJob` | `true` |

## 上下文注入机制

框架提供两种模式来管理发送给 LLM 的对话上下文：

### 模式 1：启用摘要注入（推荐）

```go
llmagent.WithAddSessionSummary(true)
```

**工作方式**：

- 会话摘要**合并到已有的系统消息中**（如果存在），否则作为新的系统消息插入到开头
- 这确保了与要求单条系统消息位于开头的模型兼容（如 Qwen3.5 系列）
- 包含摘要时间点之后的**所有增量事件**（不截断）
- 保证完整上下文：浓缩历史 + 完整新对话
- **`WithMaxHistoryRuns` 参数被忽略**

#### 摘要注入模式

默认情况下，摘要以 **system message** 的方式注入（合并到已有 system prompt 中）。这种方式下，摘要会被 token tailoring 的 preserved head 保护，不会被滑动窗口裁剪掉。

如果希望摘要能参与 token 预算裁剪，形成真正的**滑动窗口**效果，可以将注入模式切换为 `user`：

```go
agent := llmagent.New(
    "my-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithAddSessionSummary(true),
    llmagent.WithSessionSummaryInjectionMode(llmagent.SessionSummaryInjectionUser),
)
```

**两种注入模式的区别**：

| 模式 | 注入位置 | Token Tailoring 行为 | 适用场景 |
| --- | --- | --- | --- |
| `SessionSummaryInjectionSystem`（默认） | 合并到 system message | 摘要在 preserved head 中，不会被裁剪 | 需要摘要始终存在的场景 |
| `SessionSummaryInjectionUser` | 优先合并到第一条 user history/current message；否则在靠近 history 的位置注入 | 摘要参与普通轮次裁剪，可被滑动窗口淘汰 | 超长对话的滑动窗口场景 |

**User 模式的消息结构**：

当 history 第一条消息为 user role 时，摘要会自动合并进去：

```text
┌─────────────────────────────────────────┐
│ System Prompt                           │ ← 不包含摘要
├─────────────────────────────────────────┤
│ [Few-shot examples, if any]             │
├─────────────────────────────────────────┤
│ User: [summary context] + [original    │
│        first user message]              │ ← 摘要合并到第一条 user history
├─────────────────────────────────────────┤
│ Assistant: ...                          │
│ User: ...                               │
│ ...                                     │
│ User: current message                   │
└─────────────────────────────────────────┘
```

当 history 第一条消息不是 user role 时，摘要作为独立 user message 插入：

```text
┌─────────────────────────────────────────┐
│ System Prompt                           │ ← 不包含摘要
├─────────────────────────────────────────┤
│ [Few-shot examples, if any]             │
├─────────────────────────────────────────┤
│ User: Context from previous             │
│ interactions: <summary>...</summary>    │ ← 独立的摘要 user message
├─────────────────────────────────────────┤
│ Assistant/Tool history events           │
│ ...                                     │
│ User: current message                   │
└─────────────────────────────────────────┘
```

**注意事项**：

- User 模式下，processor 会优先把摘要合并到第一条 user history/current message，让摘要贴近当前生效的 user 轮次
- 如果没有可合并的 user history/current message，但 prompt 前缀最后一条已经是 user message（例如 injected context），则会回退合并到那条 user message，避免额外再插入一条相邻的 user block
- User 模式使用更中性的默认文案（"Context from previous interactions"），避免以系统指令的语气出现在 user role 中
- 自定义的 `WithSummaryFormatter` 同样对 user 模式生效
- 摘要的**生成链路不受影响**——注入模式只影响 prompt assembly 层，不影响 summarizer 本身

> **提示**：如果你的场景是超长对话（数百轮），且希望旧摘要能被自然淘汰（被新的摘要替代），建议使用 `SessionSummaryInjectionUser` 模式。

**可选：Prompt 侧上下文压缩**

当开启 `WithEnableContextCompaction(true)` 时，框架会在真正调用模型前执行两遍压缩：

**Pass 1 — 历史 tool result 占位替换**（`ContextCompactionToolResultMaxTokens`，默认 1024 tokens）：

- 只作用于**旧 request** 中超过阈值的 `tool result`，将其内容整体替换为简短占位符，但保留 `ToolID` 和 `ToolName`
- 当前 request 和最近 `ContextCompactionKeepRecentRequests` 个已完成 request 不受影响
- 适合清理已不重要的历史工具输出

**Pass 2 — 超大 tool result 截断**（`ContextCompactionOversizedToolResultMaxTokens`，默认 8192 tokens）：

- 作用于**所有 tool result，包括当前 request 的**
- 超过阈值的 tool result 会使用首尾保留策略截断：保留内容的开头和结尾，中间插入 `[...N characters truncated...]` 标记
- 这是防止单个超大 tool result 直接撑爆 context window 的安全网（例如 `web_fetch` 返回 800K+ 字符的 HTML）

两遍压缩的定位不同：Pass 1 低阈值、全量替换，激进清理旧历史；Pass 2 高阈值、只在极端情况触发，但能保护当前 request。

Pass 2 独立于 `EnableContextCompaction`，只要 `ContextCompactionOversizedToolResultMaxTokens > 0` 就会生效。

此外：

- 如果同时开启了 `WithAddSessionSummary(true)`，并且压完后请求仍接近 context window，会在 LLM 调用前同步执行一次 `CreateSessionSummary(...)` 并重建 request
- 模型层的 token tailoring 仍然作为最后兜底

```go
agent := llmagent.New(
    "my-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithAddSessionSummary(true),
    llmagent.WithEnableContextCompaction(true),
    llmagent.WithContextCompactionThresholdRatio(0.7),
    llmagent.WithContextCompactionToolResultMaxTokens(1024),  // Pass 1: 旧 tool result → 占位符
    llmagent.WithContextCompactionOversizedToolResultMaxTokens(8192),  // Pass 2: 任意超大 result → 首尾保留截断
    llmagent.WithContextCompactionKeepRecentRequests(1),
)
```

**上下文结构**：

```
┌─────────────────────────────────────────┐
│ System Prompt                           │
│ (merged with Session Summary)           │ ← 系统提示 + 浓缩历史
├─────────────────────────────────────────┤
│ Event 1 (after summary)                 │ ┐
│ Event 2                                 │ │
│ Event 3                                 │ │ 摘要后的新事件
│ ...                                     │ │ (完整保留)
│ Event N (current message)               │ ┘
└─────────────────────────────────────────┘
```

**模型兼容性**：

部分 LLM 提供商对系统消息的位置和数量有严格要求：

- **Qwen3.5 系列**等模型要求系统消息必须位于对话开头，且不支持多条系统消息
- 默认的合并行为可避免 `System message must be at the beginning` 等错误
- 预加载的内存内容也会通过相同机制合并到系统消息中

### 模式 2：不使用摘要

```go
llmagent.WithAddSessionSummary(false)
llmagent.WithMaxHistoryRuns(10)  // 限制历史轮次
```

**工作方式**：

- 不添加摘要消息
- 只包含最近 `MaxHistoryRuns` 轮对话
- `MaxHistoryRuns=0` 时不限制，包含所有历史
- 如果开启 `WithEnableContextCompaction(true)`，保留下来的旧 request 中超长 `tool result` 仍可在 request projection 阶段被压缩；此外只要 `ContextCompactionOversizedToolResultMaxTokens > 0`（即使未开启 `EnableContextCompaction`），任意 request 中的超大 tool result 也会被首尾保留截断
- 这个模式下不会触发 pre-LLM 的同步摘要重试

**上下文结构**：

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

### 模式选择建议

| 场景 | 推荐配置 | 说明 |
| --- | --- | --- |
| 长期会话（客服、助手） | `AddSessionSummary=true` | 保持完整上下文，优化 token |
| 短期会话（单次咨询） | `AddSessionSummary=false`<br>`MaxHistoryRuns=10` | 简单直接，无需摘要开销 |
| 调试测试 | `AddSessionSummary=false`<br>`MaxHistoryRuns=5` | 快速验证，减少干扰 |
| 高并发场景 | `AddSessionSummary=true`<br>增加 worker 数量 | 异步处理，不影响响应速度 |

如果你的长会话里经常出现搜索结果、日志、代码扫描输出这类长 `tool result`，建议开启 `EnableContextCompaction=true`。如果你还希望在接近 context window 时多一次同步摘要兜底，再配合 `AddSessionSummary=true` 一起使用。

> **提示**：如果你的 agent 使用了 `web_fetch` 等可能单次返回超大结果的工具，`ContextCompactionOversizedToolResultMaxTokens` 尤为重要——它能防止单个 tool result 吃光整个 context window，即使该 result 属于当前正在处理的（受保护的）request。它独立于 `EnableContextCompaction`，默认开启。

## 摘要格式自定义

默认情况下，会话摘要会以包含上下文标签和关于优先考虑当前对话信息的提示进行格式化：

**默认格式**：

```
Here is a brief summary of your previous interactions:

<summary_of_previous_interactions>
[Summary content]
</summary_of_previous_interactions>

Note: this information is from previous interactions and may be outdated. You should ALWAYS prefer information from this conversation over the past summary.
```

您可以使用 `WithSummaryFormatter` 来自定义摘要格式：

```go
agent := llmagent.New(
    "my-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithAddSessionSummary(true),
    llmagent.WithSummaryFormatter(func(summary string) string {
        return fmt.Sprintf("## Previous Context\n\n%s", summary)
    }),
)
```

**使用场景**：

- **简化格式**：使用简洁的标题和最少的上下文提示来减少 token 消耗
- **语言本地化**：将上下文提示翻译为目标语言
- **角色特定格式**：为不同的 Agent 角色提供不同的格式
- **模型优化**：根据特定模型的偏好调整格式

## 获取摘要

```go
// 获取完整会话摘要（默认）
summaryText, found := sessionService.GetSessionSummaryText(ctx, sess)
if found {
    fmt.Printf("摘要: %s\n", summaryText)
}

// 获取特定 filter key 的摘要
userSummary, found := sessionService.GetSessionSummaryText(
    ctx, sess, session.WithSummaryFilterKey("user-messages"),
)
if found {
    fmt.Printf("用户消息摘要: %s\n", userSummary)
}
```

**Filter Key 支持**：

- 不提供选项时，返回全量会话摘要（`SummaryFilterKeyAllContents`）
- 提供特定 filter key 但未找到时，回退到全量会话摘要
- 如果都不存在，兜底返回任意可用的摘要

## 按事件类型生成摘要

在实际应用中，你可能希望为不同类型的事件生成独立的摘要。

### 使用 AppendEventHook 设置 FilterKey

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

### FilterKey 前缀规范

**⚠️ 重要：FilterKey 必须添加 `appName + "/"` 前缀。**

**原因**：Runner 在过滤事件时使用 `appName + "/"` 作为过滤前缀，如果 FilterKey 没有这个前缀，事件会被过滤掉。

```go
// ✅ 正确：带 appName 前缀
evt.FilterKey = "my-app/user-messages"

// ❌ 错误：没有前缀，事件会被过滤掉
evt.FilterKey = "user-messages"
```

### 为不同类型生成摘要

```go
// 为用户消息生成摘要
err := sessionService.CreateSessionSummary(ctx, sess, "my-app/user-messages", false)

// 为工具调用生成摘要
err := sessionService.CreateSessionSummary(ctx, sess, "my-app/tool-calls", false)

// 获取特定类型的摘要
userSummary, found := sessionService.GetSessionSummaryText(
    ctx, sess, session.WithSummaryFilterKey("my-app/user-messages"))
```

## 工作原理

1. **增量处理**：摘要器跟踪每个会话的上次摘要时间，后续运行只处理上次摘要后发生的事件
2. **增量摘要**：新事件与先前的摘要组合，生成一个既包含旧上下文又包含新信息的更新摘要
3. **触发条件评估**：在生成摘要之前，评估配置的触发条件。如果条件未满足且 `force=false`，则跳过摘要
4. **异步 Worker**：摘要任务使用基于哈希的分发策略分配到多个 worker goroutine，确保同一会话的任务按顺序处理
5. **回退机制**：如果异步入队失败（队列已满、上下文取消或 worker 未初始化），系统会自动回退到同步处理

## 最佳实践

1. **选择合适的阈值**：根据 LLM 的上下文窗口和对话模式设置事件/token 阈值。对于 GPT-4（8K 上下文），考虑使用 `WithTokenThreshold(4000)` 为响应留出空间
2. **使用异步处理**：在生产环境中始终使用 `EnqueueSummaryJob` 而不是 `CreateSessionSummary`，以避免阻塞对话流程
3. **监控队列大小**：如果频繁看到"queue is full"警告，请增加 `WithSummaryQueueSize` 或 `WithAsyncSummaryNum`
4. **自定义提示词**：根据应用需求定制摘要提示词。例如，如果你正在构建客户支持 Agent，应关注关键问题和解决方案
5. **平衡字数限制**：设置 `WithMaxSummaryWords` 以在保留上下文和减少 token 使用之间取得平衡。典型值范围为 100-300 字
6. **测试触发条件**：尝试不同的 `WithChecksAny` 和 `WithChecksAll` 组合，找到摘要频率和成本之间的最佳平衡

## 性能考虑

- **LLM 成本**：每次摘要生成都会调用 LLM，监控触发条件以平衡成本和上下文保留
- **内存使用**：摘要与事件一起存储，配置适当的 TTL 以管理长时间运行会话中的内存
- **异步 Worker**：更多 worker 会提高吞吐量但消耗更多资源，从 2-4 个 worker 开始，根据负载进行扩展
- **队列容量**：根据预期的并发量和摘要生成时间调整队列大小

## 完整示例

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

    // Create LLM model for chat and summary
    llm := openai.New("gpt-4", openai.WithAPIKey("your-api-key"))

    // Create summarizer with flexible trigger conditions
    summarizer := summary.NewSummarizer(
        llm,
        summary.WithMaxSummaryWords(200),
        summary.WithChecksAny(
            summary.CheckEventThreshold(20),
            summary.CheckTokenThreshold(4000),
            summary.CheckTimeThreshold(5*time.Minute), // 在摘要检查时判断；比较被检查 session 的最后一个事件（在增量摘要路径里通常就是最近一个待摘要事件）
        ),
    )

    // Create session service with summarizer
    sessionService := inmemory.NewSessionService(
        inmemory.WithSummarizer(summarizer),
        inmemory.WithAsyncSummaryNum(2),
        inmemory.WithSummaryQueueSize(100),
        inmemory.WithSummaryJobTimeout(60*time.Second),
    )

    // Create agent with summary injection enabled
    agent := llmagent.New(
        "my-agent",
        llmagent.WithModel(llm),
        llmagent.WithAddSessionSummary(true),
        llmagent.WithMaxHistoryRuns(10),
    )

    // Create runner
    r := runner.NewRunner("my-app", agent,
        runner.WithSessionService(sessionService))

    // Run conversation - summary will be managed automatically
    userMsg := model.NewUserMessage("Tell me about AI")
    eventChan, _ := r.Run(ctx, "user123", "session456", userMsg)

    // Consume events
    for event := range eventChan {
        _ = event
    }
}
```

## 参考资源

- [摘要示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary)
- [FilterKey 示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary/filterkey)
- [注入模式示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary/injection)
