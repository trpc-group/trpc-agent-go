# 会话摘要（Summary）

## 概述

随着对话持续增长，维护完整的事件历史可能会占用大量内存，并可能超出 LLM 的上下文窗口限制。会话摘要功能使用 LLM 自动将历史对话压缩为简洁的摘要，在保留重要上下文的同时显著降低内存占用和 token 消耗。

## 核心特性

- **自动触发**：根据事件数量、token 数量或时间阈值自动生成摘要
- **增量处理**：只处理自上次摘要以来的新事件，避免重复计算
- **LLM 驱动**：使用任何配置的 LLM 模型生成高质量、上下文感知的摘要
- **非破坏性**：原始事件完整保留，摘要单独存储
- **异步处理**：后台异步执行，不阻塞对话流程
- **灵活配置**：支持自定义触发条件、提示词和字数限制

## 快速配置

```go
import (
    "time"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// 1. Create summarizer
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithChecksAny(                         // Any condition triggers summary
        summary.CheckEventThreshold(20),           // Trigger after 20 events since last summary
        summary.CheckTokenThreshold(4000),         // Trigger after 4000 tokens since last summary
        summary.CheckTimeThreshold(5*time.Minute), // Trigger after 5 minutes of inactivity
    ),
    summary.WithMaxSummaryWords(200),              // Limit summary to 200 words
)

// 2. Configure session service
sessionService := inmemory.NewSessionService(
    inmemory.WithSummarizer(summarizer),
    inmemory.WithAsyncSummaryNum(2),               // 2 async workers
    inmemory.WithSummaryQueueSize(100),            // Queue size 100
)

// 3. Enable summary injection in Agent
llmAgent := llmagent.New(
    "my-agent",
    llmagent.WithModel(llm),
    llmagent.WithAddSessionSummary(true),          // Enable summary injection
)

// 4. Create Runner
r := runner.NewRunner("my-agent", llmAgent,
    runner.WithSessionService(sessionService))
```

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

## 摘要器选项

### 触发条件

| 选项 | 说明 |
| --- | --- |
| `WithEventThreshold(eventCount int)` | 当自上次摘要后的事件数量超过阈值时触发 |
| `WithTokenThreshold(tokenCount int)` | 当自上次摘要后的 token 数量超过阈值时触发 |
| `WithTimeThreshold(interval time.Duration)` | 当自上次事件后经过的时间超过间隔时触发 |

### 组合条件

| 选项 | 说明 |
| --- | --- |
| `WithChecksAll(checks ...Checker)` | 要求所有条件都满足（AND 逻辑），使用 `Check*` 函数 |
| `WithChecksAny(checks ...Checker)` | 任何条件满足即触发（OR 逻辑），使用 `Check*` 函数 |

**注意**：在 `WithChecksAll` 和 `WithChecksAny` 中使用 `Check*` 函数（如 `CheckEventThreshold`），而不是 `With*` 函数。

```go
// AND logic: all conditions must be met
summary.WithChecksAll(
    summary.CheckEventThreshold(10),
    summary.CheckTokenThreshold(2000),
)

// OR logic: any condition triggers
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
| `WithSkipRecent(skipFunc SkipRecentFunc)` | 自定义函数跳过最近事件 |

### Hook 选项

| 选项 | 说明 |
| --- | --- |
| `WithPreSummaryHook(h PreSummaryHook)` | 摘要前的 Hook，可修改输入文本 |
| `WithPostSummaryHook(h PostSummaryHook)` | 摘要后的 Hook，可修改输出摘要 |
| `WithSummaryHookAbortOnError(abort bool)` | Hook 报错时是否中断，默认 `false`（忽略错误） |

## Checker 函数

Checker 是用于判断是否需要触发摘要的函数类型：

```go
type Checker func(sess *session.Session) bool
```

### 内置 Checker

| Checker | 说明 |
| --- | --- |
| `CheckEventThreshold(eventCount int)` | 当事件总数大于阈值时返回 true |
| `CheckTimeThreshold(interval time.Duration)` | 当距离最后一个事件的时间大于间隔时返回 true |
| `CheckTokenThreshold(tokenCount int)` | 当累计 token 数大于阈值时返回 true（使用 `event.Response.Usage.TotalTokens`） |
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
- `{max_summary_words}`：当 `maxSummaryWords > 0` 时必须包含

## 跳过最近事件

使用 `WithSkipRecent` 可以在摘要时跳过最近的事件：

```go
// Skip fixed number of events
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithSkipRecent(func(_ []event.Event) int { return 2 }), // Skip last 2 events
    summary.WithEventThreshold(10),
)

// Skip events from last 5 minutes (time window)
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

// Skip only trailing tool call messages
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
        // Modify ctx.Text or ctx.Events before summarization
        return nil
    }),
    summary.WithPostSummaryHook(func(ctx *summary.PostSummaryHookContext) error {
        // Modify ctx.Summary before returning
        return nil
    }),
    summary.WithSummaryHookAbortOnError(true), // Abort on hook error (optional)
)
```

## 摘要触发机制

### 自动触发（推荐）

Runner 在每次对话完成后自动检查触发条件，满足条件时在后台异步生成摘要。

**触发时机**：

- 事件数量超过阈值（`WithEventThreshold`）
- Token 数量超过阈值（`WithTokenThreshold`）
- 距上次事件超过指定时间（`WithTimeThreshold`）
- 满足自定义组合条件（`WithChecksAny` / `WithChecksAll`）

### 手动触发

```go
// Async summary (recommended) - background processing, non-blocking
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents, // Full session summary
    false,                               // force=false, respect trigger conditions
)

// Sync summary - immediate processing, blocking
err := sessionService.CreateSessionSummary(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    false, // force=false
)

// Force async summary - ignore trigger conditions
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    true, // force=true, bypass all trigger conditions
)

// Force sync summary - immediate forced generation
err := sessionService.CreateSessionSummary(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    true, // force=true
)
```

**参数说明**：

- **filterKey**：`session.SummaryFilterKeyAllContents`（空字符串）表示对完整会话生成摘要
- **force**：
  - `false`：遵守配置的触发条件，只有满足条件才生成摘要
  - `true`：强制生成摘要，完全忽略所有触发条件检查

## 上下文注入机制

框架提供两种模式来管理发送给 LLM 的对话上下文：

### 模式 1：启用摘要注入（推荐）

```go
llmagent.WithAddSessionSummary(true)
```

**工作方式**：

- 会话摘要作为独立的系统消息插入到第一个现有系统消息之后
- 包含摘要时间点之后的**所有增量事件**（不截断）
- 保证完整上下文：浓缩历史 + 完整新对话
- **`WithMaxHistoryRuns` 参数被忽略**

**上下文结构**：

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

### 模式 2：不使用摘要

```go
llmagent.WithAddSessionSummary(false)
llmagent.WithMaxHistoryRuns(10)  // Limit history runs
```

**工作方式**：

- 不添加摘要消息
- 只包含最近 `MaxHistoryRuns` 轮对话
- `MaxHistoryRuns=0` 时不限制，包含所有历史

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
// Get full session summary (default)
summaryText, found := sessionService.GetSessionSummaryText(ctx, sess)
if found {
    fmt.Printf("Summary: %s\n", summaryText)
}

// Get summary for specific filter key
userSummary, found := sessionService.GetSessionSummaryText(
    ctx, sess, session.WithSummaryFilterKey("user-messages"),
)
if found {
    fmt.Printf("User messages summary: %s\n", userSummary)
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
        // Classify events by author
        prefix := "my-app/"  // Must add appName prefix
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
// ✅ Correct: with appName prefix
evt.FilterKey = "my-app/user-messages"

// ❌ Wrong: no prefix, event will be filtered out
evt.FilterKey = "user-messages"
```

### 为不同类型生成摘要

```go
// Generate summary for user messages
err := sessionService.CreateSessionSummary(ctx, sess, "my-app/user-messages", false)

// Generate summary for tool calls
err := sessionService.CreateSessionSummary(ctx, sess, "my-app/tool-calls", false)

// Get summary for specific type
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

## 参考资源

- [摘要示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary)
- [FilterKey 示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary/filterkey)
