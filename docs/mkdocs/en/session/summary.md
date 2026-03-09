# Session Summary

## Overview

As conversations grow, maintaining complete event history can consume significant memory and may exceed the LLM's context window limit. The session summary feature uses LLM to automatically compress historical conversations into concise summaries, significantly reducing memory usage and token consumption while preserving important context.

## Key Features

- **Auto-trigger**: Automatically generates summaries based on event count, token count, or time thresholds
- **Incremental processing**: Only processes new events since the last summary, avoiding redundant computation
- **LLM-driven**: Uses any configured LLM model to generate high-quality, context-aware summaries
- **Non-destructive**: Original events are fully preserved; summaries are stored separately
- **Async processing**: Executes asynchronously in the background without blocking conversation flow
- **Flexible configuration**: Supports custom trigger conditions, prompts, and word limits

## Basic Configuration

### Step 1: Create Summarizer

Create a summarizer with an LLM model and configure trigger conditions:

```go
import (
    "time"

    "trpc.group/trpc-go/trpc-agent-go/session/summary"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

summaryModel := openai.New("gpt-4", openai.WithAPIKey("your-api-key"))

summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithChecksAny(
        summary.CheckEventThreshold(20),
        summary.CheckTokenThreshold(4000),
        summary.CheckTimeThreshold(5*time.Minute),
    ),
    summary.WithMaxSummaryWords(200),
)
```

### Step 2: Configure Session Service

Integrate the summarizer into a session service:

```go
import (
    "time"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

// Memory storage (dev/test)
sessionService := inmemory.NewSessionService(
    inmemory.WithSummarizer(summarizer),
    inmemory.WithAsyncSummaryNum(2),
    inmemory.WithSummaryQueueSize(100),
    inmemory.WithSummaryJobTimeout(60*time.Second),
)

// Redis storage (production)
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithSummarizer(summarizer),
    redis.WithAsyncSummaryNum(4),
    redis.WithSummaryQueueSize(200),
)

// PostgreSQL storage
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithPassword("your-password"),
    postgres.WithSummarizer(summarizer),
    postgres.WithAsyncSummaryNum(2),
    postgres.WithSummaryQueueSize(100),
)

// MySQL storage
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSummarizer(summarizer),
    mysql.WithAsyncSummaryNum(2),
    mysql.WithSummaryQueueSize(100),
)

// ClickHouse storage
sessionService, err := clickhouse.NewService(
    clickhouse.WithClickHouseDSN("clickhouse://default:password@localhost:9000/default"),
    clickhouse.WithSummarizer(summarizer),
    clickhouse.WithAsyncSummaryNum(2),
)
```

### Step 3: Configure Agent and Runner

Create an Agent and configure summary injection behavior:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

llmAgent := llmagent.New(
    "my-agent",
    llmagent.WithModel(summaryModel),
    llmagent.WithAddSessionSummary(true),
    llmagent.WithMaxHistoryRuns(10),
)

r := runner.NewRunner(
    "my-agent",
    llmAgent,
    runner.WithSessionService(sessionService),
)

eventChan, err := r.Run(ctx, userID, sessionID, userMessage)
```

After completing the above configuration, the summary feature runs automatically.

## SessionSummarizer Interface

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

## Summarizer Options

### Trigger Conditions

| Option | Description |
| --- | --- |
| `WithEventThreshold(eventCount int)` | Trigger when event count since last summary exceeds threshold |
| `WithTokenThreshold(tokenCount int)` | Trigger when token count since last summary exceeds threshold |
| `WithTimeThreshold(interval time.Duration)` | Trigger when time since last event exceeds interval |

### Combined Conditions

| Option | Description |
| --- | --- |
| `WithChecksAll(checks ...Checker)` | All conditions must be met (AND logic), use `Check*` functions |
| `WithChecksAny(checks ...Checker)` | Any condition triggers (OR logic), use `Check*` functions |

**Note**: Use `Check*` functions (e.g., `CheckEventThreshold`) inside `WithChecksAll` and `WithChecksAny`, not `With*` functions.

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

### Summary Generation

| Option | Description |
| --- | --- |
| `WithMaxSummaryWords(maxWords int)` | Limit summary word count; included in prompt to guide model |
| `WithPrompt(prompt string)` | Custom summary prompt; must contain `{conversation_text}` placeholder |
| `WithSkipRecent(skipFunc SkipRecentFunc)` | Custom function to skip recent events |

### Hook Options

| Option | Description |
| --- | --- |
| `WithPreSummaryHook(h PreSummaryHook)` | Pre-summary hook; can modify input text |
| `WithPostSummaryHook(h PostSummaryHook)` | Post-summary hook; can modify output summary |
| `WithSummaryHookAbortOnError(abort bool)` | Whether to abort on hook error; default `false` (ignore errors) |

### Tool Call Formatting

By default, the summarizer includes tool calls and tool results in the conversation text sent to the LLM for summarization. The default format is:

- Tool calls: `[Called tool: toolName with args: {"arg": "value"}]`
- Tool results: `[toolName returned: result content]`

| Option | Description |
| --- | --- |
| `WithToolCallFormatter(f ToolCallFormatter)` | Customize how tool calls are formatted in summary input. Return empty string to exclude |
| `WithToolResultFormatter(f ToolResultFormatter)` | Customize how tool results are formatted in summary input. Return empty string to exclude |

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

### Model Callbacks (Before/After Model)

The `summarizer` supports model callbacks around the underlying `model.GenerateContent` call, useful for modifying requests, short-circuiting with custom responses, or instrumentation.

| Option | Description |
| --- | --- |
| `WithModelCallbacks(callbacks *model.Callbacks)` | Register Before/After callbacks for the summarizer's underlying model calls |

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

## Checker Functions

Checker is a function type for determining whether to trigger summarization:

```go
type Checker func(sess *session.Session) bool
```

### Built-in Checkers

| Checker | Description |
| --- | --- |
| `CheckEventThreshold(eventCount int)` | Returns true when the number of delta events since the last summary exceeds the threshold |
| `CheckTimeThreshold(interval time.Duration)` | Returns true when time since last event exceeds interval |
| `CheckTokenThreshold(tokenCount int)` | Returns true when the estimated token count of delta events since the last summary exceeds the threshold (estimated via `TokenCounter` from extracted conversation text, not `event.Response.Usage.TotalTokens`) |
| `ChecksAll(checks []Checker)` | Combines multiple Checkers; returns true only when all return true (AND) |
| `ChecksAny(checks []Checker)` | Combines multiple Checkers; returns true when any returns true (OR) |

## Custom Prompt

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

**Required placeholders**:

- `{conversation_text}`: Must be included; replaced with conversation content
- `{max_summary_words}`: Must be included when `maxSummaryWords > 0`

## Token Counter Configuration

By default, `CheckTokenThreshold` uses a built-in `SimpleTokenCounter` that estimates token count based on text length. To customize token counting behavior, use `summary.SetTokenCounter` to set a global token counter:

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

**Notes**:

- **Global effect**: `SetTokenCounter` affects all `CheckTokenThreshold` evaluations in the current process; set it once during application initialization
- **Default counter**: If not set, the default `SimpleTokenCounter` is used (approximately 4 characters per token)

## Skip Recent Events

Use `WithSkipRecent` to skip recent events during summarization:

```go
// Skip a fixed number of events
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithSkipRecent(func(_ []event.Event) int { return 2 }),
    summary.WithEventThreshold(10),
)

// Skip events from the last 5 minutes (time window)
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

## Summary Hooks

### PreSummaryHook

Called before summary generation; can modify input text or events:

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

Called after summary generation; can modify the output summary:

```go
type PostSummaryHookContext struct {
    Ctx     context.Context
    Session *session.Session
    Summary string
}

type PostSummaryHook func(in *PostSummaryHookContext) error
```

### Usage Example

```go
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithPreSummaryHook(func(ctx *summary.PreSummaryHookContext) error {
        // Modify ctx.Text or ctx.Events before summary generation
        return nil
    }),
    summary.WithPostSummaryHook(func(ctx *summary.PostSummaryHookContext) error {
        // Modify ctx.Summary after summary generation
        return nil
    }),
    summary.WithSummaryHookAbortOnError(true),
)
```

## Summary Trigger Mechanism

### Automatic Trigger (Recommended)

The Runner automatically checks trigger conditions after each conversation completes, generating summaries asynchronously in the background when conditions are met.

**Trigger timing**:

- Event count exceeds threshold (`WithEventThreshold`)
- Token count exceeds threshold (`WithTokenThreshold`)
- Time since last event exceeds interval (`WithTimeThreshold`)
- Custom combined conditions met (`WithChecksAny` / `WithChecksAll`)

### Manual Trigger

In some scenarios, you may need to manually trigger summarization:

```go
// Async summary (recommended) - background processing, non-blocking
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    false,
)

// Sync summary - immediate processing, blocks current operation
err := sessionService.CreateSessionSummary(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    false,
)

// Async forced summary - ignores trigger conditions
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    true,
)

// Sync forced summary - immediate forced generation
err := sessionService.CreateSessionSummary(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    true,
)
```

**API description:**

- **`EnqueueSummaryJob`**: Async summary (recommended)
  - Background processing, non-blocking
  - Auto-fallback to sync on failure
  - Suitable for production

- **`CreateSessionSummary`**: Sync summary
  - Immediate processing, blocks current operation
  - Returns result directly
  - Suitable for debugging or when immediate results are needed

**Parameter description:**

- **filterKey**: `session.SummaryFilterKeyAllContents` generates a summary for the full session
- **force parameter**:
  - `false`: Respects configured trigger conditions; only generates summary when conditions are met
  - `true`: Forces summary generation, completely ignoring all trigger condition checks

**Use cases:**

| Scenario | Recommended API | force |
| --- | --- | --- |
| Normal conversation flow | Auto-trigger (no call needed) | - |
| Background batch processing | `EnqueueSummaryJob` | `false` |
| User-initiated request | `EnqueueSummaryJob` | `true` |
| Debug/Test | `CreateSessionSummary` | `true` |
| Session end | `EnqueueSummaryJob` | `true` |

## Context Injection Mechanism

The framework provides two modes for managing conversation context sent to the LLM:

### Mode 1: Enable Summary Injection (Recommended)

```go
llmagent.WithAddSessionSummary(true)
```

**How it works**:

- Session summary is **merged into the existing system message** if one exists, or prepended as a new system message if none exists
- This ensures compatibility with models that require a single system message at the beginning (e.g., Qwen3.5 series)
- Includes **all incremental events** after the summary point (no truncation)
- Guarantees complete context: compressed history + full new conversation
- **`WithMaxHistoryRuns` parameter is ignored**

**Context structure**:

```
┌─────────────────────────────────────────┐
│ System Prompt                           │
│ (merged with Session Summary)           │ ← System prompt + compressed history
├─────────────────────────────────────────┤
│ Event 1 (after summary)                 │ ┐
│ Event 2                                 │ │
│ Event 3                                 │ │ New events after summary
│ ...                                     │ │ (fully retained)
│ Event N (current message)               │ ┘
└─────────────────────────────────────────┘
```

**Model Compatibility**:

Some LLM providers have strict requirements for system message placement and count:

- **Qwen3.5 series** and similar models require the system message to be at the beginning and do not support multiple system messages
- The default merging behavior prevents errors like `System message must be at the beginning`
- Preloaded memory content is also merged into the system message using the same mechanism

### Mode 2: Without Summary

```go
llmagent.WithAddSessionSummary(false)
llmagent.WithMaxHistoryRuns(10)
```

**How it works**:

- No summary message added
- Only includes the most recent `MaxHistoryRuns` conversation turns
- `MaxHistoryRuns=0` means no limit, includes all history

**Context structure**:

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

### Mode Selection Guide

| Scenario | Recommended Config | Description |
| --- | --- | --- |
| Long sessions (support, assistant) | `AddSessionSummary=true` | Maintain full context, optimize tokens |
| Short sessions (single consultation) | `AddSessionSummary=false`<br>`MaxHistoryRuns=10` | Simple and direct, no summary overhead |
| Debug/Test | `AddSessionSummary=false`<br>`MaxHistoryRuns=5` | Quick validation, reduce noise |
| High concurrency | `AddSessionSummary=true`<br>Increase worker count | Async processing, no impact on response speed |

## Summary Format Customization

By default, session summaries are formatted with context tags and a note about prioritizing current conversation information:

**Default format**:

```
Here is a brief summary of your previous interactions:

<summary_of_previous_interactions>
[Summary content]
</summary_of_previous_interactions>

Note: this information is from previous interactions and may be outdated. You should ALWAYS prefer information from this conversation over the past summary.
```

You can use `WithSummaryFormatter` to customize the summary format:

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

**Use cases**:

- **Simplified format**: Use concise titles and minimal context hints to reduce token consumption
- **Language localization**: Translate context hints to the target language
- **Role-specific format**: Provide different formats for different Agent roles
- **Model optimization**: Adjust format based on specific model preferences

## Retrieving Summaries

```go
// Get full session summary (default)
summaryText, found := sessionService.GetSessionSummaryText(ctx, sess)
if found {
    fmt.Printf("Summary: %s\n", summaryText)
}

// Get summary for a specific filter key
userSummary, found := sessionService.GetSessionSummaryText(
    ctx, sess, session.WithSummaryFilterKey("user-messages"),
)
if found {
    fmt.Printf("User message summary: %s\n", userSummary)
}
```

**Filter Key support**:

- When no option is provided, returns the full session summary (`SummaryFilterKeyAllContents`)
- When a specific filter key is provided but not found, falls back to the full session summary
- If neither exists, falls back to any available summary

## Summary by Event Type

In practice, you may want to generate independent summaries for different types of events.

### Setting FilterKey with AppendEventHook

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
        prefix := "my-app/"
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

### FilterKey Prefix Convention

**Important: FilterKey must include the `appName + "/"` prefix.**

**Reason**: The Runner uses `appName + "/"` as the filter prefix when filtering events. If the FilterKey doesn't have this prefix, events will be filtered out.

```go
// Correct: with appName prefix
evt.FilterKey = "my-app/user-messages"

// Wrong: no prefix, events will be filtered out
evt.FilterKey = "user-messages"
```

### Generating Summaries by Type

```go
// Generate summary for user messages
err := sessionService.CreateSessionSummary(ctx, sess, "my-app/user-messages", false)

// Generate summary for tool calls
err := sessionService.CreateSessionSummary(ctx, sess, "my-app/tool-calls", false)

// Get summary for a specific type
userSummary, found := sessionService.GetSessionSummaryText(
    ctx, sess, session.WithSummaryFilterKey("my-app/user-messages"))
```

## How It Works

1. **Incremental processing**: The summarizer tracks the last summary time for each session; subsequent runs only process events after the last summary
2. **Incremental summary**: New events are combined with the previous summary to generate an updated summary containing both old context and new information
3. **Trigger condition evaluation**: Before generating a summary, configured trigger conditions are evaluated. If conditions are not met and `force=false`, summarization is skipped
4. **Async workers**: Summary tasks are distributed to multiple worker goroutines using a hash-based distribution strategy, ensuring tasks for the same session are processed in order
5. **Fallback mechanism**: If async enqueue fails (queue full, context cancelled, or workers not initialized), the system automatically falls back to synchronous processing

## Best Practices

1. **Choose appropriate thresholds**: Set event/token thresholds based on the LLM's context window and conversation patterns. For GPT-4 (8K context), consider `WithTokenThreshold(4000)` to leave room for responses
2. **Use async processing**: Always use `EnqueueSummaryJob` instead of `CreateSessionSummary` in production to avoid blocking conversation flow
3. **Monitor queue size**: If you frequently see "queue is full" warnings, increase `WithSummaryQueueSize` or `WithAsyncSummaryNum`
4. **Customize prompts**: Tailor summary prompts to your application needs. For example, if building a customer support Agent, focus on key issues and solutions
5. **Balance word limits**: Set `WithMaxSummaryWords` to balance context preservation and token usage. Typical range is 100-300 words
6. **Test trigger conditions**: Experiment with different `WithChecksAny` and `WithChecksAll` combinations to find the optimal balance between summary frequency and cost

## Performance Considerations

- **LLM cost**: Each summary generation calls the LLM. Monitor trigger conditions to balance cost and context preservation
- **Memory usage**: Summaries are stored alongside events. Configure appropriate TTL to manage memory in long-running sessions
- **Async workers**: More workers increase throughput but consume more resources. Start with 2-4 workers and scale based on load
- **Queue capacity**: Adjust queue size based on expected concurrency and summary generation time

## Complete Example

Here is a complete example demonstrating how all components work together:

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
            summary.CheckTimeThreshold(5*time.Minute),
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

## References

- [Summary Examples](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary)
- [FilterKey Examples](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary/filterkey)
