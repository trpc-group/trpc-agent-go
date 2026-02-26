# Session Management

## Overview

tRPC-Agent-Go provides powerful session management capabilities to maintain conversation history and context information during Agent-user interactions. Through automatic persistence of conversation records, intelligent summary compression, and flexible storage backends, session management offers complete infrastructure for building stateful intelligent Agents.

### Positioning

A Session manages the context of the current conversation, with isolation dimensions `<appName, userID, SessionID>`. It stores user messages, Agent responses, tool call results, and brief summaries generated based on this content within the conversation, supporting multi-turn question-and-answer scenarios.

Within the same conversation, it allows for seamless transitions between multiple turns of question-and-answer, preventing users from restating the same question or providing the same parameters in each turn.

### 🎯 Key Features

- **Context Management**: Automatically load conversation history for true multi-turn dialogues
- **Session Summary**: Automatically compress long conversation history using LLM while preserving key context and significantly reducing token consumption
- **Event Limiting**: Control maximum number of events stored per session to prevent memory overflow
- **TTL Management**: Support automatic expiration and cleanup of session data
- **Multiple Storage Backends**: Support Memory, Redis, PostgreSQL, MySQL, ClickHouse storage
- **Concurrency Safety**: Built-in read-write locks ensure safe concurrent access
- **Automatic Management**: Automatically handle session creation, loading, and updates after Runner integration
- **Soft Delete Support**: PostgreSQL/MySQL support soft delete with data recovery capability

## Quick Start

### Integration with Runner

tRPC-Agent-Go's session management integrates with Runner through `runner.WithSessionService`. Runner automatically handles session creation, loading, updates, and persistence.

**Supported Storage Backends:** Memory, Redis, PostgreSQL, MySQL, ClickHouse

**Default Behavior:** If `runner.WithSessionService` is not configured, Runner defaults to using memory storage (Memory), and data will be lost after process restarts.

### Basic Example

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
    "trpc.group/trpc-go/trpc-agent-go/session/summary" // Optional: required when enabling summary feature
)

func main() {
    // 1. Create LLM model
    llm := openai.New("gpt-4", openai.WithAPIKey("your-api-key"))

    // 2. (Optional) Create summarizer - automatically compress long conversation history
    summarizer := summary.NewSummarizer(
        llm, // Use same LLM model for summary generation
        summary.WithChecksAny(                         // Trigger when any condition is met
            summary.CheckEventThreshold(20),           // Trigger when 20+ new events since last summary
            summary.CheckTokenThreshold(4000),         // Trigger when 4000+ new tokens since last summary
            summary.CheckTimeThreshold(5*time.Minute), // Trigger after 5 minutes of inactivity
        ),
        summary.WithMaxSummaryWords(200), // Limit summary to 200 words
    )

    // 3. Create Session Service (optional, defaults to memory storage if not configured)
    sessionService := inmemory.NewSessionService(
        inmemory.WithSummarizer(summarizer),     // Optional: inject summarizer
        inmemory.WithAsyncSummaryNum(2),         // Optional: 2 async workers
        inmemory.WithSummaryQueueSize(100),      // Optional: queue size 100
    )

    // 4. Create Agent
    agent := llmagent.New(
        "my-agent",
        llmagent.WithModel(llm),
        llmagent.WithInstruction("You are a helpful assistant"),
        llmagent.WithAddSessionSummary(true), // Optional: enable summary injection to context
        // Note: WithAddSessionSummary(true) ignores WithMaxHistoryRuns configuration
        // Summary includes all history, incremental events fully retained
    )

    // 5. Create Runner and inject Session Service
    r := runner.NewRunner(
        "my-agent",
        agent,
        runner.WithSessionService(sessionService),
    )

    // 6. First conversation
    ctx := context.Background()
    userMsg1 := model.NewUserMessage("My name is Alice")
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
            // Streaming output, prefer Delta.Content, fallback to Message.Content
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

    // 7. Second conversation - automatically load history, AI remembers user's name
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
            // Streaming output, prefer Delta.Content, fallback to Message.Content
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
    fmt.Println() // Output: Your name is Alice
}
```

### Runner Automatic Capabilities

After integrating Session Service, Runner automatically provides the following capabilities **without needing to manually call any Session API**:

1. **Automatic Session Creation**: Automatically create session on first conversation (generates UUID if SessionID is empty)
2. **Automatic Session Loading**: Automatically load historical context at the start of each conversation
3. **Automatic Session Updates**: Automatically save new events after conversation ends
4. **Context Continuity**: Automatically inject conversation history into LLM input for multi-turn dialogues
5. **Automatic Summary Generation** (Optional): Generate summaries asynchronously in background when trigger conditions are met, no manual intervention required

## Core Capabilities

### 1️⃣ Context Management

The core function of session management is to maintain conversation context, ensuring the Agent can remember historical interactions and provide intelligent responses based on history.

**How it Works:**

- Automatically save user input and AI responses from each conversation round
- Automatically load historical events when new conversations begin
- Runner automatically injects historical context into LLM input

**Default Behavior:** After Runner integration, context management is fully automated without manual intervention.

### 2️⃣ Session Summary

As conversations continue to grow, maintaining complete event history can consume significant memory and may exceed LLM context window limits. The session summary feature uses LLM to automatically compress historical conversations into concise summaries, significantly reducing memory usage and token consumption while preserving important context.

**Core Features:**

- **Automatic Triggering**: Automatically generate summaries based on event count, token count, or time thresholds
- **Incremental Processing**: Only process new events since the last summary, avoiding redundant computation
- **LLM-Driven**: Use configured LLM model to generate high-quality, context-aware summaries
- **Non-Destructive**: Original events are fully preserved, summaries stored separately
- **Asynchronous Processing**: Execute asynchronously in background without blocking conversation flow
- **Flexible Configuration**: Support custom trigger conditions, prompts, and word limits

**Quick Configuration:**

```go
import (
    "time"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// 1. Create summarizer
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithChecksAny(                         // Trigger when any condition is met
        summary.CheckEventThreshold(20),           // Trigger when 20+ new events since last summary
        summary.CheckTokenThreshold(4000),         // Trigger when 4000+ new tokens since last summary
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

// 3. Enable summary injection to Agent
llmAgent := llmagent.New(
    "my-agent",
    llmagent.WithModel(llm),
    llmagent.WithAddSessionSummary(true),          // Enable summary injection
)

// 4. Create Runner
r := runner.NewRunner("my-agent", llmAgent,
    runner.WithSessionService(sessionService))
```

#### Summary hooks (pre/post)

You can inject hooks to tweak summary input or output:

```go
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithPreSummaryHook(func(ctx *summary.PreSummaryHookContext) error {
        // Optionally modify ctx.Text or ctx.Events before summarization.
        return nil
    }),
    summary.WithPostSummaryHook(func(ctx *summary.PostSummaryHookContext) error {
        // Optionally modify ctx.Summary before returning to caller.
        return nil
    }),
    summary.WithSummaryHookAbortOnError(true), // Abort when hook returns error (optional).
)
```

#### Model callbacks (before/after)

The `summarizer` supports model callbacks (structured signatures) around the underlying `model.GenerateContent` call. This is useful for request mutation, short-circuiting with a custom response, or adding tracing/metrics for summary generation.

```go
import (
    "context"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

callbacks := model.NewCallbacks().
    RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
        // You may mutate args.Request, or return CustomResponse to skip the real model call.
        return nil, nil
    }).
    RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
        // You may override the response via CustomResponse.
        return nil, nil
    })

summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithModelCallbacks(callbacks),
)
```

Notes:

- Pre-hook can mutate `ctx.Text` (preferred) or `ctx.Events`; post-hook can mutate `ctx.Summary`.
- Default behavior ignores hook errors; enable abort with
  `WithSummaryHookAbortOnError(true)`.

**Context Injection Mechanism:**

After enabling summary, the framework prepends the summary as a system message to the LLM input, while including all incremental events after the summary timestamp to ensure complete context:

```
┌─────────────────────────────────────────┐
│ System Prompt                           │
├─────────────────────────────────────────┤
│ Session Summary (system message)        │ ← Compressed version of historical conversations
│ - Updated at: 2024-01-10 14:30          │   (events before updated_at)
│ - Includes: Event1 ~ Event20            │
├─────────────────────────────────────────┤
│ Event 21 (user message)                 │ ┐
│ Event 22 (assistant response)           │ │
│ Event 23 (user message)                 │ │ All new conversations after summary
│ Event 24 (assistant response)           │ │ (fully retained, no truncation)
│ ...                                     │ │
│ Event N (current message)               │ ┘
└─────────────────────────────────────────┘
```

#### Summary Format Customization

By default, session summaries are formatted with context tags and a note about preferring current conversation information:

**Default Format:**

```
Here is a brief summary of your previous interactions:

<summary_of_previous_interactions>
[summary content]
</summary_of_previous_interactions>

Note: this information is from previous interactions and may be outdated. You should ALWAYS prefer information from this conversation over the past summary.
```

You can customize the summary format using `WithSummaryFormatter` (available in `llmagent` and `graphagent`) to better match your specific use cases or model requirements.

**Custom Format Example:**

```go
// Custom formatter with simplified format
agent := llmagent.New(
    "my-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithAddSessionSummary(true),
    llmagent.WithSummaryFormatter(func(summary string) string {
        return fmt.Sprintf("## Previous Context\n\n%s", summary)
    }),
)
```

**Use Cases:**

- **Simplified Format**: Reduce token usage by using concise headings and minimal context notes
- **Language Localization**: Translate context notes to target language (e.g., Chinese, Japanese)
- **Role-Specific Formatting**: Different formats for different agent roles (assistant, researcher, coder)
- **Model Optimization**: Tailor format for specific model preferences (some models respond better to certain prompt structures)

**Important Notes:**

- The formatter function receives the raw summary text from the session and returns the formatted string
- Custom formatters should ensure the summary is clearly distinguishable from other messages
- The default format is designed to be compatible with most models and use cases
- When `WithAddSessionSummary(false)` is used, the formatter is **never invoked**

**Important Note:** When `WithAddSessionSummary(true)` is enabled, the `WithMaxHistoryRuns` parameter is ignored, and all events after the summary are fully retained.

For detailed configuration and advanced usage, see the [Session Summary](#session-summary) section.

### 3️⃣ Event Limiting (EventLimit)

Control the maximum number of events stored per session to prevent memory overflow from long conversations.

**How it Works:**

- Automatically evict oldest events (FIFO) when limit is exceeded
- Only affects storage, not business logic
- Applies to all storage backends

**Configuration Example:**

```go
// Limit each session to maximum 500 events
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(500),
)
```

**Recommended Configuration:**

| Scenario                 | Recommended Value | Description                                             |
| ------------------------ | ----------------- | ------------------------------------------------------- |
| Short-term conversations | 100-200           | Customer service, single tasks                          |
| Medium-term sessions     | 500-1000          | Daily assistant, multi-turn collaboration               |
| Long-term sessions       | 1000-2000         | Personal assistant, ongoing projects (use with summary) |
| Debug/testing            | 50-100            | Quick validation, reduce noise                          |

### 4️⃣ TTL Management (Auto-Expiration)

Support setting Time To Live (TTL) for session data, automatically cleaning expired data.

**Supported TTL Types:**

- **SessionTTL**: Expiration time for session state and events
- **AppStateTTL**: Expiration time for application-level state
- **UserStateTTL**: Expiration time for user-level state

**Configuration Example:**

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionTTL(30*time.Minute),     // Session expires after 30 minutes of inactivity
    inmemory.WithAppStateTTL(24*time.Hour),      // App state expires after 24 hours
    inmemory.WithUserStateTTL(7*24*time.Hour),   // User state expires after 7 days
)
```

**Expiration Behavior:**

| Storage Type | Expiration Mechanism                           | Auto Cleanup |
| ------------ | ---------------------------------------------- | ------------ |
| Memory       | Periodic scanning + access-time checking       | Yes          |
| Redis        | Redis native TTL                               | Yes          |
| PostgreSQL   | Periodic scanning (soft delete or hard delete) | Yes          |
| MySQL        | Periodic scanning (soft delete or hard delete) | Yes          |

## Storage Backend Comparison

tRPC-Agent-Go provides five session storage backends to meet different scenario requirements:

| Storage Type | Use Case                         |
| ------------ | -------------------------------- |
| Memory       | Development/testing, small-scale |
| Redis        | Production, distributed          |
| PostgreSQL   | Production, complex queries      |
| MySQL        | Production, complex queries      |
| ClickHouse   | Production, massive logs         |

## Memory Storage

Suitable for development environments and small-scale applications, no external dependencies required, ready to use out of the box.

### Configuration Options

- **`WithSessionEventLimit(limit int)`**: Set maximum number of events stored per session. Default is 1000, evicts old events when exceeded.
- **`WithSessionTTL(ttl time.Duration)`**: Set TTL for session state and event list. Default is 0 (no expiration).
- **`WithAppStateTTL(ttl time.Duration)`**: Set TTL for application-level state. Default is 0 (no expiration).
- **`WithUserStateTTL(ttl time.Duration)`**: Set TTL for user-level state. Default is 0 (no expiration).
- **`WithCleanupInterval(interval time.Duration)`**: Set interval for automatic cleanup of expired data. Default is 0 (auto-determined), if any TTL is configured, default cleanup interval is 5 minutes.
- **`WithSummarizer(s summary.SessionSummarizer)`**: Inject session summarizer.
- **`WithAsyncSummaryNum(num int)`**: Set number of summary processing workers. Default is 3.
- **`WithSummaryQueueSize(size int)`**: Set summary task queue size. Default is 100.
- **`WithSummaryJobTimeout(timeout time.Duration)`**: Set timeout for single summary task. Default is 60 seconds.

### Basic Configuration Example

```go
import "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

// Default configuration (development environment)
sessionService := inmemory.NewSessionService()
// Effect:
// - Each session max 1000 events
// - All data never expires
// - No automatic cleanup

// Production environment configuration
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(500),
    inmemory.WithSessionTTL(30*time.Minute),
    inmemory.WithAppStateTTL(24*time.Hour),
    inmemory.WithUserStateTTL(7*24*time.Hour),
    inmemory.WithCleanupInterval(10*time.Minute),
)
// Effect:
// - Each session max 500 events
// - Session expires after 30 minutes of inactivity
// - App state expires after 24 hours
// - User state expires after 7 days
// - Cleanup every 10 minutes
```

### Use with Summary

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Create summarizer
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithEventThreshold(20),
    summary.WithMaxSummaryWords(200),
)

// Create session service and inject summarizer
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(1000),
    inmemory.WithSummarizer(summarizer),
    inmemory.WithAsyncSummaryNum(2),
    inmemory.WithSummaryQueueSize(100),
    inmemory.WithSummaryJobTimeout(60*time.Second),
)
```

## Redis Storage

Suitable for production environments and distributed applications, provides high performance and auto-expiration capabilities.

### Configuration Options

- **`WithRedisClientURL(url string)`**: Create Redis client via URL. Format: `redis://[username:password@]host:port[/database]`.
- **`WithRedisInstance(instanceName string)`**: Use pre-configured Redis instance. Note: `WithRedisClientURL` has higher priority than `WithRedisInstance`.
- **`WithSessionEventLimit(limit int)`**: Set maximum number of events stored per session. Default is 1000.
- **`WithSessionTTL(ttl time.Duration)`**: Set TTL for session state and events. Default is 0 (no expiration).
- **`WithAppStateTTL(ttl time.Duration)`**: Set TTL for application-level state. Default is 0 (no expiration).
- **`WithUserStateTTL(ttl time.Duration)`**: Set TTL for user-level state. Default is 0 (no expiration).
- **`WithSummarizer(s summary.SessionSummarizer)`**: Inject session summarizer.
- **`WithAsyncSummaryNum(num int)`**: Set number of summary processing workers. Default is 3.
- **`WithSummaryQueueSize(size int)`**: Set summary task queue size. Default is 100.
- **`WithKeyPrefix(prefix string)`**: Set Redis key prefix. All keys will be prefixed with `prefix:`. Default is empty (no prefix).
- **`WithExtraOptions(extraOptions ...interface{})`**: Set extra options for Redis client.

### Basic Configuration Example

```go
import "trpc.group/trpc-go/trpc-agent-go/session/redis"

// Create using URL (recommended)
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://username:password@127.0.0.1:6379/0"),
    redis.WithSessionEventLimit(500),
)

// Complete production environment configuration
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379/0"),
    redis.WithSessionEventLimit(1000),
    redis.WithSessionTTL(30*time.Minute),
    redis.WithAppStateTTL(24*time.Hour),
    redis.WithUserStateTTL(7*24*time.Hour),
)
// Effect:
// - Connect to local Redis database 0
// - Each session max 1000 events
// - Session auto-expires after 30 minutes of inactivity (Redis TTL)
// - App state expires after 24 hours
// - User state expires after 7 days
// - Uses Redis native TTL mechanism, no manual cleanup needed
```

### Configuration Reuse

If multiple components need to use the same Redis instance, you can register and reuse:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage"
    "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

// Register Redis instance
redisURL := "redis://127.0.0.1:6379"
storage.RegisterRedisInstance("my-redis-instance",
    storage.WithClientBuilderURL(redisURL))

// Use in session service
sessionService, err := redis.NewService(
    redis.WithRedisInstance("my-redis-instance"),
    redis.WithSessionEventLimit(500),
)
```

### Use with Summary

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithSessionEventLimit(1000),
    redis.WithSessionTTL(30*time.Minute),

    // Summary configuration
    redis.WithSummarizer(summarizer),
    redis.WithAsyncSummaryNum(4),
    redis.WithSummaryQueueSize(200),
)
```

### Storage Structure

```
# Application data
appdata:{appName} -> Hash {key: value}

# User data
userdata:{appName}:{userID} -> Hash {key: value}

# Session data
session:{appName}:{userID} -> Hash {sessionID: SessionData(JSON)}

# Event records
events:{appName}:{userID}:{sessionID} -> SortedSet {score: timestamp, value: Event(JSON)}

# Summary data (optional)
summary:{appName}:{userID}:{sessionID}:{filterKey} -> String (JSON)
```

## PostgreSQL Storage

Suitable for production environments and applications requiring complex queries, provides full relational database capabilities.

### Configuration Options

**Connection Configuration:**

- **`WithPostgresClientDSN(dsn string)`**: PostgreSQL DSN connection string (recommended). Supports two formats:
  - Key-Value format: `host=localhost port=5432 user=postgres password=secret dbname=mydb sslmode=disable`
  - URL format: `postgres://user:password@localhost:5432/dbname?sslmode=disable`
- **`WithHost(host string)`**: PostgreSQL server address. Default is `localhost`.
- **`WithPort(port int)`**: PostgreSQL server port. Default is `5432`.
- **`WithUser(user string)`**: Database username. Default is `postgres`.
- **`WithPassword(password string)`**: Database password. Default is empty string.
- **`WithDatabase(database string)`**: Database name. Default is `postgres`.
- **`WithSSLMode(sslMode string)`**: SSL mode. Default is `disable`. Options: `disable`, `require`, `verify-ca`, `verify-full`.
- **`WithPostgresInstance(name string)`**: Use pre-configured PostgreSQL instance.

> **Priority**: `WithPostgresClientDSN` > Direct connection settings (`WithHost`, etc.) > `WithPostgresInstance`

**Session Configuration:**

- **`WithSessionEventLimit(limit int)`**: Maximum events per session. Default is 1000.
- **`WithSessionTTL(ttl time.Duration)`**: Session TTL. Default is 0 (no expiration).
- **`WithAppStateTTL(ttl time.Duration)`**: App state TTL. Default is 0 (no expiration).
- **`WithUserStateTTL(ttl time.Duration)`**: User state TTL. Default is 0 (no expiration).
- **`WithCleanupInterval(interval time.Duration)`**: TTL cleanup interval. Default is 5 minutes.
- **`WithSoftDelete(enable bool)`**: Enable or disable soft delete. Default is `true`.

**Async Persistence Configuration:**

- **`WithEnableAsyncPersist(enable bool)`**: Enable async persistence. Default is `false`.
- **`WithAsyncPersisterNum(num int)`**: Number of async persistence workers. Default is 10.

**Summary Configuration:**

- **`WithSummarizer(s summary.SessionSummarizer)`**: Inject session summarizer.
- **`WithAsyncSummaryNum(num int)`**: Number of summary processing workers. Default is 3.
- **`WithSummaryQueueSize(size int)`**: Summary task queue size. Default is 100.
- **`WithSummaryJobTimeout(timeout time.Duration)`**: Set timeout for single summary task. Default is 60 seconds.

**Schema and Table Configuration:**

- **`WithSchema(schema string)`**: Specify schema name.
- **`WithTablePrefix(prefix string)`**: Table name prefix.
- **`WithSkipDBInit(skip bool)`**: Skip automatic table creation.

### Basic Configuration Example

```go
import "trpc.group/trpc-go/trpc-agent-go/session/postgres"

// Using DSN connection (recommended)
sessionService, err := postgres.NewService(
    postgres.WithPostgresClientDSN("postgres://user:password@localhost:5432/mydb?sslmode=disable"),
)

// Or using Key-Value format DSN
sessionService, err := postgres.NewService(
    postgres.WithPostgresClientDSN("host=localhost port=5432 user=postgres password=secret dbname=mydb sslmode=disable"),
)

// Using individual configuration options (traditional way)
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithPort(5432),
    postgres.WithUser("postgres"),
    postgres.WithPassword("your-password"),
    postgres.WithDatabase("trpc_sessions"),
)

// Complete production environment configuration
sessionService, err := postgres.NewService(
    // Connection configuration (DSN recommended)
    postgres.WithPostgresClientDSN("postgres://user:password@localhost:5432/trpc_sessions?sslmode=require"),

    // Session configuration
    postgres.WithSessionEventLimit(1000),
    postgres.WithSessionTTL(30*time.Minute),
    postgres.WithAppStateTTL(24*time.Hour),
    postgres.WithUserStateTTL(7*24*time.Hour),

    // TTL cleanup configuration
    postgres.WithCleanupInterval(10*time.Minute),
    postgres.WithSoftDelete(true),  // Soft delete mode

    // Async persistence configuration
    postgres.WithAsyncPersisterNum(4),
)
// Effect:
// - Use SSL encrypted connection
// - Session expires after 30 minutes of inactivity
// - Cleanup expired data every 10 minutes (soft delete)
// - 4 async workers for writes
```

### Configuration Reuse

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
    sessionpg "trpc.group/trpc-go/trpc-agent-go/session/postgres"
)

// Register PostgreSQL instance
postgres.RegisterPostgresInstance("my-postgres-instance",
    postgres.WithClientConnString("postgres://user:password@localhost:5432/trpc_sessions?sslmode=disable"),
)

// Use in session service
sessionService, err := sessionpg.NewService(
    sessionpg.WithPostgresInstance("my-postgres-instance"),
    sessionpg.WithSessionEventLimit(500),
)
```

### Schema and Table Prefix

PostgreSQL supports schema and table prefix configuration for multi-tenant and multi-environment scenarios:

```go
// Use schema
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithDatabase("mydb"),
    postgres.WithSchema("my_schema"),  // Table name: my_schema.session_states
)

// Use table prefix
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithTablePrefix("app1_"),  // Table name: app1_session_states
)

// Combined usage
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSchema("tenant_a"),
    postgres.WithTablePrefix("app1_"),  // Table name: tenant_a.app1_session_states
)
```

**Table Naming Rules:**

| Schema      | Prefix  | Final Table Name                |
| ----------- | ------- | ------------------------------- |
| (none)      | (none)  | `session_states`                |
| (none)      | `app1_` | `app1_session_states`           |
| `my_schema` | (none)  | `my_schema.session_states`      |
| `my_schema` | `app1_` | `my_schema.app1_session_states` |

### Soft Delete and TTL Cleanup

**Soft Delete Configuration:**

```go
// Enable soft delete (default)
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSoftDelete(true),
)

// Disable soft delete (physical delete)
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSoftDelete(false),
)
```

**Delete Behavior Comparison:**

| Configuration      | Delete Operation                | Query Behavior                                                                   | Data Recovery   |
| ------------------ | ------------------------------- | -------------------------------------------------------------------------------- | --------------- |
| `softDelete=true`  | `UPDATE SET deleted_at = NOW()` | Queries include `WHERE deleted_at IS NULL`, returning only non-soft-deleted rows | Recoverable     |
| `softDelete=false` | `DELETE FROM ...`               | Query all records                                                                | Not recoverable |

**TTL Auto Cleanup:**

```go
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithSessionTTL(30*time.Minute),      // Session expires after 30 minutes
    postgres.WithAppStateTTL(24*time.Hour),       // App state expires after 24 hours
    postgres.WithUserStateTTL(7*24*time.Hour),    // User state expires after 7 days
    postgres.WithCleanupInterval(10*time.Minute), // Cleanup every 10 minutes
    postgres.WithSoftDelete(true),                // Soft delete mode
)
// Cleanup behavior:
// - softDelete=true: Expired data marked as deleted_at = NOW()
// - softDelete=false: Expired data physically deleted
// - Queries always append `WHERE deleted_at IS NULL`, returning only non-soft-deleted rows.
```

### Use with Summary

```go
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithPassword("your-password"),
    postgres.WithSessionEventLimit(1000),
    postgres.WithSessionTTL(30*time.Minute),

    // Summary configuration
    postgres.WithSummarizer(summarizer),
    postgres.WithAsyncSummaryNum(2),
    postgres.WithSummaryQueueSize(100),
)
```

### Storage Structure

PostgreSQL uses relational table structure with JSON data stored using JSONB type.

For complete table definitions, see [session/postgres/schema.sql](https://github.com/trpc-group/trpc-agent-go/blob/main/session/postgres/schema.sql)

## MySQL Storage

Suitable for production environments and applications requiring complex queries, MySQL is a widely used relational database.

### Configuration Options

**Connection Configuration:**

- **`WithMySQLClientDSN(dsn string)`**：MySQL config
- **`WithInstanceName(name string)`**: Use pre-configured MySQL instance.

**Session Configuration:**

- **`WithSessionEventLimit(limit int)`**: Maximum events per session. Default is 1000.
- **`WithSessionTTL(ttl time.Duration)`**: Session TTL. Default is 0 (no expiration).
- **`WithAppStateTTL(ttl time.Duration)`**: App state TTL. Default is 0 (no expiration).
- **`WithUserStateTTL(ttl time.Duration)`**: User state TTL. Default is 0 (no expiration).
- **`WithCleanupInterval(interval time.Duration)`**: TTL cleanup interval. Default is 5 minutes.
- **`WithSoftDelete(enable bool)`**: Enable or disable soft delete. Default is `true`.

**Async Persistence Configuration:**

- **`WithEnableAsyncPersist(enable bool)`**: Enable async persistence. Default is `false`.
- **`WithAsyncPersisterNum(num int)`**: Number of async persistence workers. Default is 10.

**Summary Configuration:**

- **`WithSummarizer(s summary.SessionSummarizer)`**: Inject session summarizer.
- **`WithAsyncSummaryNum(num int)`**: Number of summary processing workers. Default is 3.
- **`WithSummaryQueueSize(size int)`**: Summary task queue size. Default is 100.
- **`WithSummaryJobTimeout(timeout time.Duration)`**: Set timeout for single summary task. Default is 60 seconds.

**Table Configuration:**

- **`WithTablePrefix(prefix string)`**: Table name prefix.
- **`WithSkipDBInit(skip bool)`**: Skip automatic table creation.

### Basic Configuration Example

```go
import "trpc.group/trpc-go/trpc-agent-go/session/mysql"

// Default configuration (minimal)
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
)
// Effect:
// - Connect to localhost:3306, database trpc_sessions
// - Each session max 1000 events
// - Data never expires
// - Default 10 async persistence workers (tunable via WithAsyncPersisterNum)

// Complete production environment configuration
sessionService, err := mysql.NewService(
    // Connection configuration
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),

    // Session configuration
    mysql.WithSessionEventLimit(1000),
    mysql.WithSessionTTL(30*time.Minute),
    mysql.WithAppStateTTL(24*time.Hour),
    mysql.WithUserStateTTL(7*24*time.Hour),

    // TTL cleanup configuration
    mysql.WithCleanupInterval(10*time.Minute),
    mysql.WithSoftDelete(true),  // Soft delete mode

    // Async persistence configuration
    mysql.WithAsyncPersisterNum(4),
)
// Effect:
// - Session expires after 30 minutes of inactivity
// - Cleanup expired data every 10 minutes (soft delete)
// - 4 async workers for writes
```

### Configuration Reuse

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
    sessionmysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
)

// Register MySQL instance
mysql.RegisterMySQLInstance("my-mysql-instance",
    mysql.WithClientBuilderDSN("root:password@tcp(localhost:3306)/trpc_sessions?parseTime=true&charset=utf8mb4"),
)

// Use in session service
sessionService, err := sessionmysql.NewService(
    sessionmysql.WithMySQLInstance("my-mysql-instance"),
    sessionmysql.WithSessionEventLimit(500),
)
```

### Table Prefix

MySQL supports table prefix configuration for multi-application shared database scenarios:

```go
// Use table prefix
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithTablePrefix("app1_"),  // Table name: app1_session_states
)
```

### Soft Delete and TTL Cleanup

**Soft Delete Configuration:**

```go
// Enable soft delete (default)
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSoftDelete(true),
)

// Disable soft delete (physical delete)
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSoftDelete(false),
)
```

**Delete Behavior Comparison:**

| Configuration      | Delete Operation                | Query Behavior                                                                   | Data Recovery   |
| ------------------ | ------------------------------- | -------------------------------------------------------------------------------- | --------------- |
| `softDelete=true`  | `UPDATE SET deleted_at = NOW()` | Queries include `WHERE deleted_at IS NULL`, returning only non-soft-deleted rows | Recoverable     |
| `softDelete=false` | `DELETE FROM ...`               | Query all records                                                                | Not recoverable |

**TTL Auto Cleanup:**

```go
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSessionTTL(30*time.Minute),      // Session expires after 30 minutes
    mysql.WithAppStateTTL(24*time.Hour),       // App state expires after 24 hours
    mysql.WithUserStateTTL(7*24*time.Hour),    // User state expires after 7 days
    mysql.WithCleanupInterval(10*time.Minute), // Cleanup every 10 minutes
    mysql.WithSoftDelete(true),                // Soft delete mode
)
// Cleanup behavior:
// - softDelete=true: Expired data marked as deleted_at = NOW()
// - softDelete=false: Expired data physically deleted
// - Queries always append `WHERE deleted_at IS NULL`, returning only non-soft-deleted rows.
```

### Use with Summary

```go
sessionService, err := mysql.NewService(
    mysql.WithMySQLClientDSN("user:password@tcp(localhost:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"),
    mysql.WithSessionEventLimit(1000),
    mysql.WithSessionTTL(30*time.Minute),

    // Summary configuration
    mysql.WithSummarizer(summarizer),
    mysql.WithAsyncSummaryNum(2),
    mysql.WithSummaryQueueSize(100),
)
```

### Storage Structure

MySQL uses relational table structure with JSON data stored using JSON type.

For complete table definitions, see [session/mysql/schema.sql](https://github.com/trpc-group/trpc-agent-go/blob/main/session/mysql/schema.sql)

### Database Migration

#### Migrating from Older Versions

**Affected Versions**: v1.2.0 and earlier  
**Fixed in Version**: v1.2.0 and later

**Background**: Earlier versions of the `session_summaries` table had index design issues:

- The earliest version used a unique index that included the `deleted_at` column. However, in MySQL `NULL != NULL`, which means multiple records with `deleted_at = NULL` would not trigger the unique constraint.
- Later versions changed to a regular lookup index (non-unique), which also could not prevent duplicate data.

Both situations could lead to duplicate data.

**Old Index** (one of the following):

- `idx_*_session_summaries_unique_active(app_name, user_id, session_id, filter_key, deleted_at)` — unique index but includes deleted_at
- `idx_*_session_summaries_lookup(app_name, user_id, session_id, deleted_at)` — regular index

**New Index**: `idx_*_session_summaries_unique_active(app_name(191), user_id(191), session_id(191), filter_key(191))` — unique index without deleted_at (prefix indexes are used to avoid Error 1071).

**Migration Steps**:

```sql
-- ============================================================================
-- Migration Script: Fix session_summaries unique index issue
-- Please backup your data before executing!
-- ============================================================================

-- Step 1: Check current indexes to confirm old index name
SHOW INDEX FROM session_summaries;

-- Step 2: Clean up duplicate data (keep the latest record)
-- If there are multiple records with deleted_at = NULL, keep the one with the largest id.
DELETE t1 FROM session_summaries t1
INNER JOIN session_summaries t2
WHERE t1.app_name = t2.app_name
  AND t1.user_id = t2.user_id
  AND t1.session_id = t2.session_id
  AND t1.filter_key = t2.filter_key
  AND t1.deleted_at IS NULL
  AND t2.deleted_at IS NULL
  AND t1.id < t2.id;

-- Step 3: Hard delete soft-deleted records (summary data is regenerable)
-- If you need to keep soft-deleted records, skip this step, but handle conflicts
-- manually before Step 5.
DELETE FROM session_summaries WHERE deleted_at IS NOT NULL;

-- Step 4: Drop the old index (choose the correct index name based on Step 1 results)
-- Note: Index name may have a table prefix, adjust according to your configuration.
-- If it's a lookup index:
DROP INDEX idx_session_summaries_lookup ON session_summaries;
-- If it's an old unique_active index (includes deleted_at):
-- DROP INDEX idx_session_summaries_unique_active ON session_summaries;

-- Step 5: Create the new unique index (without deleted_at)
-- Note: Index name may have a table prefix, adjust according to your configuration.
CREATE UNIQUE INDEX idx_session_summaries_unique_active 
ON session_summaries(app_name(191), user_id(191), session_id(191), filter_key(191));

-- Step 6: Verify migration results
SELECT COUNT(*) as duplicate_count FROM (
    SELECT app_name, user_id, session_id, filter_key, COUNT(*) as cnt
    FROM session_summaries
    WHERE deleted_at IS NULL
    GROUP BY app_name, user_id, session_id, filter_key
    HAVING cnt > 1
) t;
-- Expected result: duplicate_count = 0

-- Step 7: Verify the index was created successfully
SHOW INDEX FROM session_summaries WHERE Key_name = 'idx_session_summaries_unique_active';
-- Expected result: Shows the newly created unique index without deleted_at column
```

**Notes**:

1. If you configured `WithTablePrefix("trpc_")`, table and index names will have a prefix:
   - Table name: `trpc_session_summaries`
   - Old index name: `idx_trpc_session_summaries_lookup` or `idx_trpc_session_summaries_unique_active`
   - New index name: `idx_trpc_session_summaries_unique_active`
   - Please adjust the table and index names in the SQL above according to your actual configuration.

2. The new index does not include the `deleted_at` column, which means soft-deleted summary records will block new records with the same business key. Since summary data is regenerable, it is recommended to hard delete soft-deleted records during migration (Step 3). If you skip this step, you need to handle conflicts manually.



## ClickHouse Storage

Suitable for production environments and massive data scenarios, leveraging ClickHouse's powerful write throughput and data compression capabilities.

### Configuration Options

**Connection Configuration:**

- **`WithClickHouseDSN(dsn string)`**: ClickHouse DSN connection string (recommended).
  - Format: `clickhouse://user:password@host:port/database?dial_timeout=10s`
- **`WithClickHouseInstance(name string)`**: Use pre-configured ClickHouse instance.
- **`WithExtraOptions(opts ...any)`**: Set extra options for ClickHouse client.

**Session Configuration:**

- **`WithSessionEventLimit(limit int)`**: Maximum events per session. Default is 1000.
- **`WithSessionTTL(ttl time.Duration)`**: Session TTL. Default is 0 (no expiration).
- **`WithAppStateTTL(ttl time.Duration)`**: App state TTL. Default is 0 (no expiration).
- **`WithUserStateTTL(ttl time.Duration)`**: User state TTL. Default is 0 (no expiration).
- **`WithDeletedRetention(retention time.Duration)`**: Retention period for soft-deleted data. Default is 0 (disable application-level physical cleanup). When enabled, it will periodically clean up soft-deleted data via `ALTER TABLE DELETE`. **Not recommended** for production environments; prefer ClickHouse table-level TTL.
- **`WithCleanupInterval(interval time.Duration)`**: Cleanup task interval.

**Async Persistence Configuration:**

- **`WithEnableAsyncPersist(enable bool)`**: Enable async persistence. Default is `false`.
- **`WithAsyncPersisterNum(num int)`**: Number of async persistence workers. Default is 10.
- **`WithBatchSize(size int)`**: Batch write size. Default is 100.
- **`WithBatchTimeout(timeout time.Duration)`**: Batch write timeout. Default is 100ms.

**Summary Configuration:**

- **`WithSummarizer(s summary.SessionSummarizer)`**: Inject session summarizer.
- **`WithAsyncSummaryNum(num int)`**: Number of summary processing workers. Default is 3.
- **`WithSummaryQueueSize(size int)`**: Summary task queue size. Default is 100.
- **`WithSummaryJobTimeout(timeout time.Duration)`**: Timeout for single summary task.

**Schema Configuration:**

- **`WithTablePrefix(prefix string)`**: Table name prefix.
- **`WithSkipDBInit(skip bool)`**: Skip automatic table creation.

**Hook Configuration:**

- **`WithAppendEventHook(hooks ...session.AppendEventHook)`**: Add hooks for event appending.
- **`WithGetSessionHook(hooks ...session.GetSessionHook)`**: Add hooks for session retrieval.

### Basic Configuration Example

```go
import "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"

// Default configuration (minimal)
sessionService, err := clickhouse.NewService(
    clickhouse.WithClickHouseDSN("clickhouse://default:password@localhost:9000/default"),
)

// Complete configuration example
sessionService, err := clickhouse.NewService(
    clickhouse.WithClickHouseDSN("clickhouse://user:pass@ch-host:9000/sessions?dial_timeout=5s"),

    // Session configuration
    clickhouse.WithSessionEventLimit(2000),
    clickhouse.WithSessionTTL(7*24*time.Hour), // Expires in 7 days

    // Enable async persistence (recommended)
    clickhouse.WithEnableAsyncPersist(true),
    clickhouse.WithAsyncPersisterNum(20),
    clickhouse.WithBatchSize(500),

    // Automatic cleanup
    clickhouse.WithCleanupInterval(1*time.Hour),
)
```

### Configuration Reuse

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
    sessionch "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
)

// Register ClickHouse instance
clickhouse.RegisterClickHouseInstance("my-clickhouse",
    clickhouse.WithClientBuilderDSN("clickhouse://localhost:9000/default"),
)

// Use in session service
sessionService, err := sessionch.NewService(
    sessionch.WithClickHouseInstance("my-clickhouse"),
)
```

### Storage Structure

ClickHouse implementation uses `ReplacingMergeTree` engine to handle data updates and deduplication.

**Key Features:**

1.  **ReplacingMergeTree**: Uses `updated_at` column as version for background deduplication, keeping the latest version.
2.  **FINAL Query**: All read operations use `FINAL` keyword (e.g., `SELECT ... FINAL`) to ensure data consistency by merging parts at query time.
3.  **Soft Delete**: Deletion is implemented by inserting a new record with `deleted_at` timestamp. Queries filter with `deleted_at IS NULL`.

```sql
-- Session states table
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

-- Session events table
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

-- Session summaries table
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

-- Application states table
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

-- User states table
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

## Advanced Usage

### Hook Capabilities (Append/Get)

- **AppendEventHook**: Intercept/modify/abort events before they are stored. Useful for content safety or auditing (e.g., tagging `violation=<word>`), or short-circuiting persistence. For filterKey usage, see the “Session Summarization / FilterKey with AppendEventHook” section below.
- **GetSessionHook**: Intercept/modify/filter sessions after they are read. Useful for removing tagged events or dynamically augmenting the returned session state.
- **Chain-of-responsibility**: Hooks call `next()` to continue; returning early short-circuits later hooks, and errors bubble up.
- **Backend parity**: Memory, Redis, MySQL, and PostgreSQL share the same hook interface—inject hook slices when constructing the service.
- **Example**: See `examples/session/hook` ([code](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/hook))

### Direct Use of Session Service API

In most cases, you should use session management through Runner, which automatically handles all details. However, in some special scenarios (such as session management backend, data migration, statistical analysis, etc.), you may need to directly operate the Session Service.

**Note:** The following APIs are only for special scenarios, daily use of Runner is sufficient.

#### Query Session List

```go
// List all sessions of a user
sessions, err := sessionService.ListSessions(ctx, session.UserKey{
    AppName: "my-agent",
    UserID:  "user123",
})

for _, sess := range sessions {
    fmt.Printf("SessionID: %s, Events: %d\n", sess.ID, len(sess.Events))
}
```

#### Manually Delete Session

```go
// Delete specified session
err := sessionService.DeleteSession(ctx, session.Key{
    AppName:   "my-agent",
    UserID:    "user123",
    SessionID: "session-id-123",
})
```

#### Manually Get Session Details

```go
// Get complete session
sess, err := sessionService.GetSession(ctx, session.Key{
    AppName:   "my-agent",
    UserID:    "user123",
    SessionID: "session-id-123",
})

// Get session with latest 10 events only
sess, err := sessionService.GetSession(ctx, key,
    session.WithEventNum(10))

// Get events after specified time
sess, err := sessionService.GetSession(ctx, key,
    session.WithEventTime(time.Now().Add(-1*time.Hour)))
```

#### Directly Append Events to Session

In some scenarios, you may want to directly append events to a session without invoking the model. This is useful for:

- Pre-loading conversation history from external sources
- Inserting system messages or context before the first user query
- Recording user actions or metadata as events
- Building conversation context programmatically

**Important**: An Event can represent both user requests and model responses. When you use `Runner.Run()`, the framework automatically creates events for both user messages and assistant responses.

**Example: Append a User Message**

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

// Create a user message
message := model.NewUserMessage("Hello, I'm learning Go programming.")

// Create event with required fields:
// - invocationID: Unique identifier (required)
// - author: Event author, "user" for user messages (required)
// - response: *model.Response with Choices containing Message (required)
invocationID := uuid.New().String()
evt := event.NewResponseEvent(
    invocationID, // Required: unique invocation identifier
    "user",       // Required: event author
    &model.Response{
        Done: false, // Recommended: false for non-final events
        Choices: []model.Choice{
            {
                Index:   0,       // Required: choice index
                Message: message, // Required: message with Content or ContentParts
            },
        },
    },
)
evt.RequestID = uuid.New().String() // Optional: for tracking

// Append event to session
if err := sessionService.AppendEvent(ctx, sess, evt); err != nil {
    return fmt.Errorf("append event failed: %w", err)
}
```

**Example: Append a System Message**

```go
systemMessage := model.Message{
    Role:    model.RoleSystem,
    Content: "You are a helpful assistant specialized in Go programming.",
}

evt := event.NewResponseEvent(
    uuid.New().String(),
    "system", // Author for system messages
    &model.Response{
        Done:    false,
        Choices: []model.Choice{{Index: 0, Message: systemMessage}},
    },
)

if err := sessionService.AppendEvent(ctx, sess, evt); err != nil {
    return err
}
```

**Example: Append an Assistant Message**

```go
assistantMessage := model.Message{
    Role:    model.RoleAssistant,
    Content: "Go is a statically typed, compiled programming language.",
}

evt := event.NewResponseEvent(
    uuid.New().String(),
    "assistant", // Author for assistant messages (or use agent name)
    &model.Response{
        Done:    false,
        Choices: []model.Choice{{Index: 0, Message: assistantMessage}},
    },
)

if err := sessionService.AppendEvent(ctx, sess, evt); err != nil {
    return err
}
```

**Event Required Fields**

When creating an event using `event.NewResponseEvent()`, the following fields are required:

1. **Function Parameters**:

   - `invocationID` (string): Unique identifier, typically `uuid.New().String()`
   - `author` (string): Event author (`"user"`, `"system"`, or agent name)
   - `response` (\*model.Response): Response object with Choices

2. **Response Fields**:

   - `Choices` ([]model.Choice): At least one Choice with `Index` and `Message`
   - `Message`: Must have `Content` or `ContentParts`

3. **Auto-generated Fields** (by `event.NewResponseEvent()`):

   - `ID`: Auto-generated UUID
   - `Timestamp`: Auto-set to current time
   - `Version`: Auto-set to `CurrentVersion`

4. **Persistence Requirements**:
   - `Response != nil`
   - `!IsPartial` (or has `StateDelta`)
   - `IsValidContent()` returns `true`

**How It Works with Runner**

When you later use `Runner.Run()` with the same session:

1. Runner automatically loads the session (including all appended events)
2. Converts session events to messages
3. Includes all messages (appended + current) in the conversation context
4. Sends everything to the model together

All appended events become part of the conversation history and are available to the model in subsequent interactions.

**Example**: See `examples/session/appendevent` ([code](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/appendevent))

## Session Summarization

### Overview

As conversations grow longer, maintaining full event history can become memory-intensive and may exceed LLM context windows. The session summarization feature automatically compresses historical conversation content into concise summaries using LLM-based summarization, reducing memory usage while preserving important context for future interactions.

### Key Features

- **Automatic summarization**: Automatically trigger summaries based on configurable conditions such as event count, token count, or time threshold.
- **Incremental summarization**: Only new events since the last summary are processed, avoiding redundant computation.
- **LLM-powered**: Uses any configured LLM model to generate high-quality, context-aware summaries.
- **Non-destructive**: Original events remain unchanged; summaries are stored separately.
- **Asynchronous processing**: Summary jobs are processed asynchronously to avoid blocking the main conversation flow.
- **Customizable prompts**: Configure custom summarization prompts and word limits.

### Basic Usage

#### Configure Summarizer

Create a summarizer with an LLM model and configure trigger conditions:

```go
import (
    "time"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Create LLM model for summarization.
summaryModel := openai.New("gpt-4", openai.WithAPIKey("your-api-key"))

// Create summarizer with trigger conditions.
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithEventThreshold(20),        // Trigger when 20+ new events since last summary.
    summary.WithTokenThreshold(4000),      // Trigger when 4000+ new tokens since last summary.
    summary.WithMaxSummaryWords(200),      // Limit summary to 200 words.
)
```

#### Integrate with Session Service

Attach the summarizer to your session service (in-memory or Redis):

```go
import (
    "time"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/session/redis"
    "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
)

// Option 1: In-memory session service with summarizer.
sessionService := inmemory.NewSessionService(
    inmemory.WithSummarizer(summarizer),
    inmemory.WithAsyncSummaryNum(2),                // 2 async workers.
    inmemory.WithSummaryQueueSize(100),             // Queue size 100.
    inmemory.WithSummaryJobTimeout(60*time.Second), // 60s timeout per job.
)

// Option 2: Redis session service with summarizer.
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithSummarizer(summarizer),
    redis.WithAsyncSummaryNum(4),           // 4 async workers.
    redis.WithSummaryQueueSize(200),        // Queue size 200.
)

// Option 3: ClickHouse session service with summarizer.
sessionService, err := clickhouse.NewService(
    clickhouse.WithClickHouseDSN("clickhouse://default:password@localhost:9000/default"),
    clickhouse.WithSummarizer(summarizer),
    clickhouse.WithAsyncSummaryNum(2),
)
```

#### Automatic Summarization in Runner

Once configured, the Runner automatically triggers summarization. You can also configure the LLM agent to use summaries in context:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// Create agent with summary injection enabled.
llmAgent := llmagent.New(
    "my-agent",
    llmagent.WithModel(summaryModel),
    llmagent.WithAddSessionSummary(true),   // Inject summary as system message.
    llmagent.WithMaxHistoryRuns(10),        // Limit history runs when AddSessionSummary=false
)

// Create runner with session service.
runner := runner.NewRunner(
    "my-agent",
    llmAgent,
    runner.WithSessionService(sessionService),
)

// Summaries are automatically created and injected during conversation.
eventChan, err := runner.Run(ctx, userID, sessionID, userMessage)
```

**How it works:**

The framework provides two distinct modes for managing conversation context sent to the LLM:

**Mode 1: With Summary (`WithAddSessionSummary(true)`)**

- The session summary is inserted as a separate system message after the first existing system message (or prepended if no system message exists).
- **All incremental events** after the summary timestamp are included (no truncation).
- This ensures complete context: condensed history (summary) + all new conversations since summarization.
- `WithMaxHistoryRuns` is **ignored** in this mode.

**Mode 2: Without Summary (`WithAddSessionSummary(false)`)**

- No summary is prepended.
- Only the **most recent `MaxHistoryRuns` conversation turns** are included.
- When `MaxHistoryRuns=0` (default), no limit is applied and all history is included.
- Use this mode for short sessions or when you want direct control over context window size.

**Context Construction Details:**

```
When AddSessionSummary = true:
┌─────────────────────────────────────┐
│ Existing System Message (optional)  │ ← If present
├─────────────────────────────────────┤
│ Session Summary (system message)    │ ← Inserted after first system message
├─────────────────────────────────────┤
│ Event 1 (after summary timestamp)   │ ┐
│ Event 2                             │ │ All incremental
│ Event 3                             │ │ events since
│ ...                                 │ │ last summary
│ Event N (current)                   │ ┘
└─────────────────────────────────────┘

When AddSessionSummary = false:
┌─────────────────────────────────────┐
│ System Prompt                       │
├─────────────────────────────────────┤
│ Event N-k+1                         │ ┐
│ Event N-k+2                         │ │ Last k turns
│ ...                                 │ │ (if MaxHistoryRuns=k)
│ Event N (current)                   │ ┘
└─────────────────────────────────────┘
```

**Best Practices:**

- For long-running sessions, use `WithAddSessionSummary(true)` to maintain full context while managing token usage.
- For short sessions or when testing, use `WithAddSessionSummary(false)` with appropriate `MaxHistoryRuns`.
- The Runner automatically enqueues async summary jobs after appending events to the session.

### Configuration Options

#### Summarizer Options

Configure the summarizer behavior with the following options:

**Trigger Conditions:**

- **`WithEventThreshold(eventCount int)`**: Trigger summarization when the number of new events since last summary exceeds the threshold. Example: `WithEventThreshold(20)` triggers when 20+ new events have occurred since last summary.
- **`WithTokenThreshold(tokenCount int)`**: Trigger summarization when the new token count since last summary exceeds the threshold. Example: `WithTokenThreshold(4000)` triggers when 4000+ new tokens have been added since last summary.
- **`WithTimeThreshold(interval time.Duration)`**: Trigger summarization when time elapsed since the last event exceeds the interval. Example: `WithTimeThreshold(5*time.Minute)` triggers after 5 minutes of inactivity.

**Composite Conditions:**

- **`WithChecksAll(checks ...Checker)`**: Require all conditions to be met (AND logic). Use with `Check*` functions (not `With*`). Example:
  ```go
  summary.WithChecksAll(
      summary.CheckEventThreshold(10),
      summary.CheckTokenThreshold(2000),
  )
  ```
- **`WithChecksAny(checks ...Checker)`**: Trigger if any condition is met (OR logic). Use with `Check*` functions (not `With*`). Example:
  ```go
  summary.WithChecksAny(
      summary.CheckEventThreshold(50),
      summary.CheckTimeThreshold(10*time.Minute),
  )
  ```

**Note:** Use `Check*` functions (like `CheckEventThreshold`) inside `WithChecksAll` and `WithChecksAny`. Use `With*` functions (like `WithEventThreshold`) as direct options to `NewSummarizer`. The `Check*` functions create checker instances, while `With*` functions are option setters.

**Summary Generation:**

- **`WithMaxSummaryWords(maxWords int)`**: Limit the summary to a maximum word count. The limit is included in the prompt to guide the model's generation. Example: `WithMaxSummaryWords(150)` requests summaries within 150 words.
- **`WithPrompt(prompt string)`**: Provide a custom summarization prompt. The prompt must include the placeholder `{conversation_text}`, which will be replaced with the conversation content. Optionally include `{max_summary_words}` for word limit instructions.
- **`WithSkipRecent(skipFunc SkipRecentFunc)`**: Skip the _most recent_ events during summarization using a custom function. The function receives all events and returns how many tail events to skip. Return 0 to skip none. Useful for avoiding summarizing very recent/incomplete conversations, or applying time/content-based skipping strategies.

#### Token Counter Configuration

By default, `CheckTokenThreshold` uses a built-in `SimpleTokenCounter` to estimate tokens based on text length. If you need to customize the token counting behavior (e.g., using a more accurate tokenizer for specific models), you can use `summary.SetTokenCounter` to set a global token counter:

```go
import (
    "context"
    "fmt"
    "unicode/utf8"

    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Set custom token counter (affects all CheckTokenThreshold evaluations)
summary.SetTokenCounter(model.NewSimpleTokenCounter())

// Or use custom implementation
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

// Create summarizer with token threshold checker
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.CheckTokenThreshold(4000),  // Will use your custom counter
)
```

**Important Notes:**

- **Global Effect**: `SetTokenCounter` affects all `CheckTokenThreshold` evaluations in the current process. Set it once during application initialization.
- **Default Counter**: If not set, a `SimpleTokenCounter` with default configuration is used (approx. 4 characters per token).
- **Use Cases**:
  - Use accurate tokenizers (tiktoken) when precise estimation is needed
  - Adjust for language-specific models (Chinese models may have different token densities)
  - Integrate with model-specific token counting APIs for better accuracy

**Tool Call Formatting:**

By default, the summarizer includes tool calls and tool results in the conversation text sent to the LLM for summarization. The default format is:

- Tool calls: `[Called tool: toolName with args: {"arg": "value"}]`
- Tool results: `[toolName returned: result content]`

You can customize how tool calls and results are formatted using these options:

- **`WithToolCallFormatter(f ToolCallFormatter)`**: Customize how tool calls are formatted in the summary input. The formatter receives a `model.ToolCall` and returns a formatted string. Return empty string to exclude the tool call.
- **`WithToolResultFormatter(f ToolResultFormatter)`**: Customize how tool results are formatted in the summary input. The formatter receives the `model.Message` containing the tool result and returns a formatted string. Return empty string to exclude the result.

**Example with custom tool formatters:**

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
        return "" // Return empty to exclude tool results.
    }),
    summary.WithEventThreshold(20),
)

// Only include tool names, exclude arguments
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

**Example with custom prompt:**

```go
customPrompt := `Analyze the following conversation and provide a concise summary
focusing on key decisions, action items, and important context.
Keep it within {max_summary_words} words.

<conversation>
{conversation_text}
</conversation>

Summary:`

summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithPrompt(customPrompt), // Custom Prompt
    summary.WithMaxSummaryWords(100), // Inject into {max_summary_words}
    summary.WithEventThreshold(15),
)

// Skip a fixed number of recent events (compatible with old behavior)
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithSkipRecent(func(_ []event.Event) int { return 2 }), // skip last 2 events
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

// Skip trailing tool-call messages only
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

#### Session Service Options

Configure async summary processing in session services:

- **`WithSummarizer(s summary.SessionSummarizer)`**: Inject the summarizer into the session service.
- **`WithAsyncSummaryNum(num int)`**: Set the number of async worker goroutines for summary processing. Default is 3. More workers allow higher concurrency but consume more resources.
- **`WithSummaryQueueSize(size int)`**: Set the size of the summary job queue. Default is 100. Larger queues allow more pending jobs but consume more memory.
- **`WithSummaryJobTimeout(timeout time.Duration)`**: Set the timeout for processing a single summary job. Default is 60 seconds.

### Manual Summarization

You can manually trigger summarization using the session service APIs:

```go
// Asynchronous summarization (recommended) - background processing, non-blocking.
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents, // Full session summary.
    false,                                // force=false, respects trigger conditions.
)

// Synchronous summarization - immediate processing, blocking.
err := sessionService.CreateSessionSummary(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    false, // force=false, respects trigger conditions.
)

// Asynchronous force summarization - bypass trigger conditions.
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    true, // force=true, bypass all trigger condition checks.
)

// Synchronous force summarization - immediate forced generation.
err := sessionService.CreateSessionSummary(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    true, // force=true, bypass all trigger condition checks.
)
```

**API Description:**

- **`EnqueueSummaryJob`**: Asynchronous summarization (recommended)

  - Background processing, non-blocking
  - Automatic fallback to sync processing on failure
  - Suitable for production environments

- **`CreateSessionSummary`**: Synchronous summarization
  - Immediate processing, blocking current operation
  - Direct result return
  - Suitable for debugging or scenarios requiring immediate results

**Parameter Description:**

- **filterKey**: `session.SummaryFilterKeyAllContents` indicates generating summary for the complete session
- **force parameter**:
  - `false`: Respects configured trigger conditions (event count, token count, time threshold, etc.), only generates summary when conditions are met
  - `true`: Forces summary generation, completely ignores all trigger condition checks, executes regardless of session state

**Usage Scenarios:**

| Scenario                   | API                            | force   | Description                                      |
| -------------------------- | ------------------------------ | ------- | ------------------------------------------------ |
| Normal auto-summary        | Automatically called by Runner | `false` | Auto-generates when trigger conditions met       |
| Session end                | `EnqueueSummaryJob`            | `true`  | Force generate final complete summary            |
| User requests view         | `CreateSessionSummary`         | `true`  | Immediately generate and return                  |
| Scheduled batch processing | `EnqueueSummaryJob`            | `false` | Batch check and process qualified sessions       |
| Debug/testing              | `CreateSessionSummary`         | `true`  | Immediate execution, convenient for verification |

### Retrieve Summary

Get the latest summary text from a session:

```go
// Get the full-session summary (default behavior)
summaryText, found := sessionService.GetSessionSummaryText(ctx, sess)
if found {
    fmt.Printf("Summary: %s\n", summaryText)
}

// Get summary for a specific filter key
userSummary, found := sessionService.GetSessionSummaryText(
    ctx, sess, session.WithSummaryFilterKey("user-messages"),
)
if found {
    fmt.Printf("User messages summary: %s\n", userSummary)
}
```

**Filter Key Support:**

The `GetSessionSummaryText` method supports an optional `WithSummaryFilterKey` option to retrieve summaries for specific event filters:

- When no option is provided, returns the full-session summary (`SummaryFilterKeyAllContents`)
- When a specific filter key is provided but not found, falls back to the full-session summary
- If neither exists, returns any available summary as a last resort

### How It Works

1. **Incremental Processing**: The summarizer tracks the last summarization time for each session. On subsequent runs, it only processes events that occurred after the last summary.

2. **Delta Summarization**: New events are combined with the previous summary (prepended as a system event) to generate an updated summary that incorporates both old context and new information.

3. **Trigger Evaluation**: Before generating a summary, the summarizer evaluates configured trigger conditions (based on incremental event count, token count, and time threshold since last summary). If conditions aren't met and `force=false`, summarization is skipped.

4. **Async Workers**: Summary jobs are distributed across multiple worker goroutines using hash-based distribution. This ensures jobs for the same session are processed sequentially while different sessions can be processed in parallel.

5. **Fallback Mechanism**: If async enqueueing fails (queue full, context cancelled, or workers not initialized), the system automatically falls back to synchronous processing.

### Best Practices

1. **Choose appropriate thresholds**: Set event/token thresholds based on your LLM's context window and conversation patterns. For GPT-4 (8K context), consider `WithTokenThreshold(4000)` to leave room for responses.

2. **Use async processing**: Always use `EnqueueSummaryJob` instead of `CreateSessionSummary` in production to avoid blocking the conversation flow.

3. **Monitor queue sizes**: If you see frequent "queue is full" warnings, increase `WithSummaryQueueSize` or `WithAsyncSummaryNum`.

4. **Customize prompts**: Tailor the summarization prompt to your application's needs. For example, if you're building a customer support agent, focus on key issues and resolutions.

5. **Balance word limits**: Set `WithMaxSummaryWords` to balance between preserving context and reducing token usage. Typical values range from 100-300 words.

6. **Test trigger conditions**: Experiment with different combinations of `WithChecksAny` and `WithChecksAll` to find the right balance between summary frequency and cost.

### Summarizing by Event Type

In real-world applications, you may want to generate separate summaries for different types of events. For example:

- **User Message Summary**: Summarize user needs and questions
- **Tool Call Summary**: Record which tools were used and their results
- **System Event Summary**: Track system state changes

To achieve this, you need to set the `FilterKey` field on events to identify their type.

#### Setting FilterKey with AppendEventHook

The recommended approach is to use `AppendEventHook` to automatically set `FilterKey` before events are persisted:

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
        // Auto-categorize by event author
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

Once FilterKey is set, you can generate independent summaries for different event types:

```go
// Generate summary for user messages
err := sessionService.CreateSessionSummary(ctx, sess, "my-app/user-messages", false)

// Generate summary for tool calls
err := sessionService.CreateSessionSummary(ctx, sess, "my-app/tool-calls", false)

// Retrieve summary for specific type
userSummary, found := sessionService.GetSessionSummaryText(
    ctx, sess, session.WithSummaryFilterKey("my-app/user-messages"))
```

#### FilterKey Prefix Convention

**⚠️ Important: FilterKey must include the `appName + "/"` prefix.**

**Why:** The Runner uses `appName + "/"` as the filter prefix when filtering events. If your FilterKey lacks this prefix, events will be filtered out, causing:

- LLM cannot see conversation history, may repeatedly trigger tool calls
- Summary content is incomplete, losing important context

**Example:**

```go
// ✅ Correct: with appName prefix
evt.FilterKey = "my-app/user-messages"

// ❌ Wrong: no prefix, events will be filtered out
evt.FilterKey = "user-messages"
```

**Technical Details:** The framework uses prefix matching (`strings.HasPrefix`) to determine which events should be included in the context. See `ContentRequestProcessor` filtering logic for details.

#### Complete Examples

See the following examples for complete FilterKey usage scenarios:

- [examples/session/hook](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/hook) - Hook basics
- [examples/summary/filterkey](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary/filterkey) - Summarizing by FilterKey

### Performance Considerations

- **LLM costs**: Each summary generation calls the LLM. Monitor your trigger conditions to balance cost and context preservation.
- **Memory usage**: Summaries are stored in addition to events. Configure appropriate TTLs to manage memory in long-running sessions.
- **Async workers**: More workers increase throughput but consume more resources. Start with 2-4 workers and scale based on load.
- **Queue capacity**: Size the queue based on your expected concurrency and summary generation time.

### Complete Example

Here's a complete example demonstrating all components together:

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

    // Create LLM model for both chat and summarization.
    llm := openai.New("gpt-4", openai.WithAPIKey("your-api-key"))

    // Create summarizer with flexible trigger conditions.
    summarizer := summary.NewSummarizer(
        llm,
        summary.WithMaxSummaryWords(200),
        summary.WithChecksAny(
            summary.CheckEventThreshold(20),
            summary.CheckTokenThreshold(4000),
            summary.CheckTimeThreshold(5*time.Minute),
        ),
    )

    // Create session service with summarizer.
    sessionService := inmemory.NewSessionService(
        inmemory.WithSummarizer(summarizer),
        inmemory.WithAsyncSummaryNum(2),
        inmemory.WithSummaryQueueSize(100),
        inmemory.WithSummaryJobTimeout(60*time.Second),
    )

    // Create agent with summary injection enabled.
    agent := llmagent.New(
        "my-agent",
        llmagent.WithModel(llm),
        llmagent.WithAddSessionSummary(true),
        llmagent.WithMaxHistoryRuns(10),        // Limit history runs when AddSessionSummary=false
    )

    // Create runner.
    r := runner.NewRunner("my-app", agent,
        runner.WithSessionService(sessionService))

    // Run conversation - summaries are automatically managed.
    userMsg := model.NewUserMessage("Tell me about AI")
    eventChan, _ := r.Run(ctx, "user123", "session456", userMsg)

    // Consume events.
    for event := range eventChan {
        // Handle events...
    }
}
```

## References

- [Session example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner)
- [Summary example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary)

By properly using session management, in combination with session summarization mechanisms, you can build stateful intelligent Agents that maintain conversation context while efficiently managing memory, providing users with continuous and personalized interaction experiences while ensuring the long-term sustainability of your system.
