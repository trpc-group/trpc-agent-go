# Session Management

## Overview

tRPC-Agent-Go provides powerful session management capabilities to maintain conversation history and context information during Agent-user interactions. Through automatic persistence of conversation records, intelligent summary compression, and flexible storage backends, session management offers complete infrastructure for building stateful intelligent Agents.

### 🎯 Key Features

- **Context Management**: Automatically load conversation history for true multi-turn dialogues
- **Session Summary**: Automatically compress long conversation history using LLM while preserving key context and significantly reducing token consumption
- **Event Limiting**: Control maximum number of events stored per session to prevent memory overflow
- **TTL Management**: Support automatic expiration and cleanup of session data
- **Multiple Storage Backends**: Support Memory, Redis, PostgreSQL, MySQL storage
- **Concurrency Safety**: Built-in read-write locks ensure safe concurrent access
- **Automatic Management**: Automatically handle session creation, loading, and updates after Runner integration
- **Soft Delete Support**: PostgreSQL/MySQL support soft delete with data recovery capability

## Quick Start

### Integration with Runner

tRPC-Agent-Go's session management integrates with Runner through `runner.WithSessionService`. Runner automatically handles session creation, loading, updates, and persistence.

**Supported Storage Backends:** Memory, Redis, PostgreSQL, MySQL

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
    ctx := context.Background()

    // 1. Create LLM model
    llm, err := openai.NewModel(
        openai.WithAPIKey("your-api-key"),
        openai.WithModelName("gpt-4"),
    )
    if err != nil {
        panic(err)
    }

    // 2. (Optional) Create summarizer - automatically compress long conversation history
    summarizer := summary.NewSummarizer(
        llm, // Use same LLM model for summary generation
        summary.WithChecksAny(                         // Trigger when any condition is met
            summary.CheckEventThreshold(20),           // Trigger after 20 events
            summary.CheckTokenThreshold(4000),         // Trigger after 4000 tokens
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
        llmagent.WithSystemPrompt("You are a helpful assistant"),
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
        summary.CheckEventThreshold(20),           // Trigger after 20 events
        summary.CheckTokenThreshold(4000),         // Trigger after 4000 tokens
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

tRPC-Agent-Go provides four session storage backends to meet different scenario requirements:

| Storage Type | Use Case                         | Advantages                                             | Disadvantages                               |
| ------------ | -------------------------------- | ------------------------------------------------------ | ------------------------------------------- |
| Memory       | Development/testing, small-scale | Simple and fast, no external dependencies              | Data not persistent, no distributed support |
| Redis        | Production, distributed          | High performance, distributed support, auto-expiration | Requires Redis service                      |
| PostgreSQL   | Production, complex queries      | Relational database, supports complex queries, JSONB   | Relatively heavy, requires database         |
| MySQL        | Production, complex queries      | Widely used, supports complex queries, JSON            | Relatively heavy, requires database         |

## Memory Storage

Suitable for development environments and small-scale applications, no external dependencies required, ready to use out of the box.

### Configuration Options

- **`WithSessionEventLimit(limit int)`**: Set maximum number of events stored per session. Default is 1000, evicts old events when exceeded.
- **`WithSessionTTL(ttl time.Duration)`**: Set TTL for session state and event list. Default is 0 (no expiration).
- **`WithAppStateTTL(ttl time.Duration)`**: Set TTL for application-level state. Default is 0 (no expiration).
- **`WithUserStateTTL(ttl time.Duration)`**: Set TTL for user-level state. Default is 0 (no expiration).
- **`WithCleanupInterval(interval time.Duration)`**: Set interval for automatic cleanup of expired data. Default is 0 (auto-determined), if any TTL is configured, default cleanup interval is 5 minutes.
- **`WithSummarizer(s summary.SessionSummarizer)`**: Inject session summarizer.
- **`WithAsyncSummaryNum(num int)`**: Set number of summary processing workers. Default is 2.
- **`WithSummaryQueueSize(size int)`**: Set summary task queue size. Default is 100.
- **`WithSummaryJobTimeout(timeout time.Duration)`**: Set timeout for single summary task. Default is 30 seconds.

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
    inmemory.WithSummaryJobTimeout(30*time.Second),
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
- **`WithAsyncSummaryNum(num int)`**: Set number of summary processing workers. Default is 2.
- **`WithSummaryQueueSize(size int)`**: Set summary task queue size. Default is 100.
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

- **`WithHost(host string)`**: PostgreSQL server address. Default is `localhost`.
- **`WithPort(port int)`**: PostgreSQL server port. Default is `5432`.
- **`WithUser(user string)`**: Database username. Default is `postgres`.
- **`WithPassword(password string)`**: Database password. Default is empty string.
- **`WithDatabase(database string)`**: Database name. Default is `postgres`.
- **`WithSSLMode(sslMode string)`**: SSL mode. Default is `disable`. Options: `disable`, `require`, `verify-ca`, `verify-full`.
- **`WithInstanceName(name string)`**: Use pre-configured PostgreSQL instance.

**Session Configuration:**

- **`WithSessionEventLimit(limit int)`**: Maximum events per session. Default is 1000.
- **`WithSessionTTL(ttl time.Duration)`**: Session TTL. Default is 0 (no expiration).
- **`WithAppStateTTL(ttl time.Duration)`**: App state TTL. Default is 0 (no expiration).
- **`WithUserStateTTL(ttl time.Duration)`**: User state TTL. Default is 0 (no expiration).
- **`WithCleanupInterval(interval time.Duration)`**: TTL cleanup interval. Default is 5 minutes.
- **`WithSoftDelete(enable bool)`**: Enable or disable soft delete. Default is `true`.

**Async Persistence Configuration:**

- **`WithAsyncPersisterNum(num int)`**: Number of async persistence workers. Default is 2.
- **`WithPersistQueueSize(size int)`**: Persistence task queue size. Default is 1000.

**Summary Configuration:**

- **`WithSummarizer(s summary.SessionSummarizer)`**: Inject session summarizer.
- **`WithAsyncSummaryNum(num int)`**: Number of summary processing workers. Default is 2.
- **`WithSummaryQueueSize(size int)`**: Summary task queue size. Default is 100.

**Schema and Table Configuration:**

- **`WithSchema(schema string)`**: Specify schema name.
- **`WithTablePrefix(prefix string)`**: Table name prefix.
- **`WithSkipDBInit()`**: Skip automatic table creation.

### Basic Configuration Example

```go
import "trpc.group/trpc-go/trpc-agent-go/session/postgres"

// Default configuration (minimal)
sessionService, err := postgres.NewService(
    postgres.WithHost("localhost"),
    postgres.WithPassword("your-password"),
)
// Effect:
// - Connect to localhost:5432, database postgres
// - Each session max 1000 events
// - Data never expires
// - 2 async persistence workers

// Complete production environment configuration
sessionService, err := postgres.NewService(
    // Connection configuration
    postgres.WithHost("localhost"),
    postgres.WithPort(5432),
    postgres.WithUser("postgres"),
    postgres.WithPassword("your-password"),
    postgres.WithDatabase("trpc_sessions"),
    postgres.WithSSLMode("require"),

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
    postgres.WithPersistQueueSize(2000),
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
    "trpc.group/trpc-go/trpc-agent-go/storage"
    "trpc.group/trpc-go/trpc-agent-go/session/postgres"
)

// Register PostgreSQL instance
storage.RegisterPostgresInstance("my-postgres-instance",
    storage.WithPostgresHost("localhost"),
    storage.WithPostgresPort(5432),
    storage.WithPostgresUser("postgres"),
    storage.WithPostgresPassword("your-password"),
    storage.WithPostgresDatabase("trpc_sessions"),
)

// Use in session service
sessionService, err := postgres.NewService(
    postgres.WithInstanceName("my-postgres-instance"),
    postgres.WithSessionEventLimit(500),
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

| Configuration      | Delete Operation                | Query Behavior              | Data Recovery   |
| ------------------ | ------------------------------- | --------------------------- | --------------- |
| `softDelete=true`  | `UPDATE SET deleted_at = NOW()` | Filter `deleted_at IS NULL` | Recoverable     |
| `softDelete=false` | `DELETE FROM ...`               | Query all records           | Not recoverable |

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
// - Queries always filter deleted_at IS NULL
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

PostgreSQL uses relational table structure with JSON data stored using JSONB type:

```sql
-- Session states table
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

-- Partial unique index (only applies to non-deleted records)
CREATE UNIQUE INDEX idx_session_states_unique_active
ON session_states(app_name, user_id, session_id)
WHERE deleted_at IS NULL;

-- Session events table
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

-- Session summaries table
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

-- Application states table
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

-- User states table
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

- **`WithAsyncPersisterNum(num int)`**: Number of async persistence workers. Default is 2.
- **`WithPersistQueueSize(size int)`**: Persistence task queue size. Default is 1000.

**Summary Configuration:**

- **`WithSummarizer(s summary.SessionSummarizer)`**: Inject session summarizer.
- **`WithAsyncSummaryNum(num int)`**: Number of summary processing workers. Default is 2.
- **`WithSummaryQueueSize(size int)`**: Summary task queue size. Default is 100.

**Table Configuration:**

- **`WithTablePrefix(prefix string)`**: Table name prefix.
- **`WithSkipDBInit()`**: Skip automatic table creation.

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
// - 2 async persistence workers

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
    mysql.WithPersistQueueSize(2000),
)
// Effect:
// - Session expires after 30 minutes of inactivity
// - Cleanup expired data every 10 minutes (soft delete)
// - 4 async workers for writes
```

### Configuration Reuse

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage"
    "trpc.group/trpc-go/trpc-agent-go/session/mysql"
)

// Register MySQL instance
storage.RegisterMySQLInstance("my-mysql-instance",
    storage.WithMySQLHost("localhost"),
    storage.WithMySQLPort(3306),
    storage.WithMySQLUser("root"),
    storage.WithMySQLPassword("your-password"),
    storage.WithMySQLDatabase("trpc_sessions"),
)

// Use in session service
sessionService, err := mysql.NewService(
    mysql.WithInstanceName("my-mysql-instance"),
    mysql.WithSessionEventLimit(500),
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

| Configuration      | Delete Operation                | Query Behavior              | Data Recovery   |
| ------------------ | ------------------------------- | --------------------------- | --------------- |
| `softDelete=true`  | `UPDATE SET deleted_at = NOW()` | Filter `deleted_at IS NULL` | Recoverable     |
| `softDelete=false` | `DELETE FROM ...`               | Query all records           | Not recoverable |

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
// - Queries always filter deleted_at IS NULL
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

MySQL uses relational table structure with JSON data stored using JSON type:

```sql
-- Session states table
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

-- Session events table
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

-- Session summaries table
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

-- Application states table
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

-- User states table
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

**Key Differences Between MySQL and PostgreSQL:**

- MySQL doesn't support partial index with `WHERE deleted_at IS NULL`, requires including `deleted_at` in unique index
- MySQL uses `JSON` type instead of `JSONB` (similar functionality, different storage format)
- MySQL uses `ON DUPLICATE KEY UPDATE` syntax for UPSERT

## Advanced Usage

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

## Session Summary

### Overview

As conversations continue to grow, maintaining complete event history can consume significant memory and may exceed LLM context window limits. The session summary feature uses LLM to automatically compress historical conversations into concise summaries, significantly reducing memory usage and token consumption while preserving important context.

**Core Features:**

- **Automatic Triggering**: Automatically generate summaries based on event count, token count, or time thresholds
- **Incremental Processing**: Only process new events since the last summary
- **LLM-Driven**: Use configured LLM model to generate high-quality summaries
- **Non-Destructive**: Original events fully preserved, summaries stored separately
- **Asynchronous Processing**: Execute asynchronously in background without blocking conversation flow

### Basic Configuration

#### Step 1: Create Summarizer

```go
import (
    "time"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Create LLM model for summarization
summaryModel, err := openai.NewModel(
    openai.WithAPIKey("your-api-key"),
    openai.WithModelName("gpt-4"),
)

// Create summarizer and configure trigger conditions
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithChecksAny(                     // Trigger when any condition is met
        summary.CheckEventThreshold(20),       // Trigger after 20 events
        summary.CheckTokenThreshold(4000),     // Trigger after 4000 tokens
        summary.CheckTimeThreshold(5*time.Minute), // Trigger after 5 minutes of inactivity
    ),
    summary.WithMaxSummaryWords(200),          // Limit summary to 200 words
)
```

#### Step 2: Configure Session Service

```go
// Memory storage
sessionService := inmemory.NewSessionService(
    inmemory.WithSummarizer(summarizer),
    inmemory.WithAsyncSummaryNum(2),
    inmemory.WithSummaryQueueSize(100),
    inmemory.WithSummaryJobTimeout(30*time.Second),
)

// Redis storage
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
```

#### Step 3: Configure Agent and Runner

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// Create Agent (enable summary injection)
llmAgent := llmagent.New(
    "my-agent",
    llmagent.WithModel(summaryModel),
    llmagent.WithAddSessionSummary(true),   // Enable summary injection
)

// Create Runner
r := runner.NewRunner(
    "my-agent",
    llmAgent,
    runner.WithSessionService(sessionService),
)

// Run conversation - summary automatically managed
eventChan, err := r.Run(ctx, userID, sessionID, userMessage)
```

### Summary Trigger Mechanism

#### Automatic Triggering (Recommended)

**Runner Automatic Triggering:** After each conversation completes, Runner automatically checks trigger conditions and generates summaries asynchronously in background when conditions are met, without manual intervention.

**Trigger Timing:**

- Event count reaches threshold (`WithEventThreshold`)
- Token count reaches threshold (`WithTokenThreshold`)
- Time since last event exceeds specified duration (`WithTimeThreshold`)
- Custom composite conditions are met (`WithChecksAny` / `WithChecksAll`)

#### Manual Triggering

In some scenarios, you may need to manually trigger summary generation:

```go
// Asynchronous summary (recommended) - background processing, non-blocking
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents, // Generate summary for complete session
    false,                               // force=false, respects trigger conditions
)

// Synchronous summary - immediate processing, blocks current operation
err := sessionService.CreateSessionSummary(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    false, // force=false, respects trigger conditions
)

// Asynchronous force summary - ignore trigger conditions, force generation
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    true, // force=true, bypass all trigger condition checks
)

// Synchronous force summary - immediate force generation
err := sessionService.CreateSessionSummary(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    true, // force=true, bypass all trigger condition checks
)
```

**API Description:**

- **`EnqueueSummaryJob`**: Asynchronous summarization (recommended)
  - Background processing, doesn't block current operation
  - Automatically falls back to synchronous processing on failure
  - Suitable for production environments

- **`CreateSessionSummary`**: Synchronous summarization
  - Immediate processing, blocks current operation
  - Returns processing result directly
  - Suitable for debugging or scenarios requiring immediate results

**Parameter Description:**

- **filterKey**: `session.SummaryFilterKeyAllContents` represents generating summary for complete session
- **force parameter**:
  - `false`: Respects configured trigger conditions (event count, token count, time threshold, etc.), only generates summary when conditions are met
  - `true`: Force summary generation, completely ignores all trigger condition checks, executes regardless of session state

**Use Cases:**

| Scenario | API | force | Description |
|----------|-----|-------|-------------|
| Normal auto-summary | Called automatically by Runner | `false` | Generate when trigger conditions are met |
| Session ending | `EnqueueSummaryJob` | `true` | Force generate final complete summary |
| User requests view | `CreateSessionSummary` | `true` | Generate immediately and return |
| Scheduled batch processing | `EnqueueSummaryJob` | `false` | Batch check and process sessions meeting conditions |
| Debug/testing | `CreateSessionSummary` | `true` | Execute immediately for easy validation |

### Context Injection Mechanism

**Mode 1: Enable Summary Injection (Recommended)**

```go
llmagent.WithAddSessionSummary(true)
```

- Summary automatically prepended as system message to LLM input
- Includes all incremental events after summary timestamp
- Ensures complete context: condensed history + complete new conversations
- **Important**: This mode ignores `WithMaxHistoryRuns` configuration

**Context Structure Example:**

```
┌─────────────────────────────────────────┐
│ System Prompt                           │
├─────────────────────────────────────────┤
│ Session Summary (system message)        │ ← Compressed history
│ - Updated at: 2024-01-10 14:30          │   (events before updated_at)
│ - Includes: Event1 ~ Event20            │
├─────────────────────────────────────────┤
│ Event 21 (user message)                 │ ┐
│ Event 22 (assistant response)           │ │
│ Event 23 (user message)                 │ │ Incremental events
│ Event 24 (assistant response)           │ │ (events after updated_at)
│ ...                                     │ │ (fully retained)
│ Event N (current message)               │ ┘
└─────────────────────────────────────────┘
```

**Use Cases:** Long-running sessions that need to maintain full historical context while controlling token consumption.

**Mode 2: Without Summary**

```go
llmagent.WithAddSessionSummary(false)
llmagent.WithMaxHistoryRuns(10)  // Include only last 10 conversation rounds
```

- No summary injection, directly use original events
- Limited by `WithMaxHistoryRuns`
- Suitable for short-term conversations or scenarios with large enough context windows

**Context Structure:**

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

**Use Cases:** Short sessions, testing environments, or scenarios requiring precise control of context window size.


**Mode Selection Recommendations:**

| Scenario | Recommended Configuration | Description |
|----------|--------------------------|-------------|
| Long-term sessions (customer service, assistant) | `AddSessionSummary=true` | Maintain full context, optimize tokens |
| Short-term sessions (single consultation) | `AddSessionSummary=false`<br>`MaxHistoryRuns=10` | Simple and direct, no summary overhead |
| Debug/testing | `AddSessionSummary=false`<br>`MaxHistoryRuns=5` | Quick validation, reduce noise |
| High concurrency scenarios | `AddSessionSummary=true`<br>Increase worker count | Async processing, no impact on response speed |

### Advanced Options

#### Summarizer Options

The summarizer supports custom prompts using a placeholder system to dynamically replace conversation content and word limits:

**Supported Placeholders:**

| Placeholder | Description | Required | Replacement Rule |
|-------------|-------------|----------|------------------|
| `{conversation_text}` | Conversation content placeholder | **Yes** | Replaced with formatted conversation history (`Author: Content` format) |
| `{max_summary_words}` | Word limit placeholder | No | If `WithMaxSummaryWords(n)` is configured, replaced with number `n`; otherwise replaced with empty string |

**Prompt Usage Rules:**

1. **Required Placeholder**: Custom prompts must include `{conversation_text}`, otherwise summary cannot be generated
2. **Default Prompt**: If no custom prompt is provided, uses built-in default prompt
3. **Dynamic Adjustment**: `{max_summary_words}` automatically adjusts based on configuration (replaced if value exists, removed if not)

#### Trigger Condition Configuration

Use the following options to configure summarizer behavior:

**Trigger Conditions:**

- **`WithEventThreshold(eventCount int)`**: Trigger summary when event count exceeds threshold. Example: `WithEventThreshold(20)` triggers after 20 events.
- **`WithTokenThreshold(tokenCount int)`**: Trigger summary when total token count exceeds threshold. Example: `WithTokenThreshold(4000)` triggers after 4000 tokens.
- **`WithTimeThreshold(interval time.Duration)`**: Trigger summary when time elapsed since last event exceeds interval. Example: `WithTimeThreshold(5*time.Minute)` triggers after 5 minutes of inactivity.

**Composite Conditions:**

- **`WithChecksAll(checks ...Checker)`**: Require all conditions to be met (AND logic). Use `Check*` functions (not `With*`). Example:
  ```go
  summary.WithChecksAll(
      summary.CheckEventThreshold(10),
      summary.CheckTokenThreshold(2000),
  )
  ```
- **`WithChecksAny(checks ...Checker)`**: Trigger when any condition is met (OR logic). Use `Check*` functions (not `With*`). Example:
  ```go
  summary.WithChecksAny(
      summary.CheckEventThreshold(50),
      summary.CheckTimeThreshold(10*time.Minute),
  )
  ```

**Note:** Use `Check*` functions (like `CheckEventThreshold`) in `WithChecksAll` and `WithChecksAny`. Use `With*` functions (like `WithEventThreshold`) as direct options for `NewSummarizer`. `Check*` functions create checker instances, while `With*` functions are option setters.

**Summary Generation:**

- **`WithMaxSummaryWords(maxWords int)`**: Limit maximum summary word count. This limit is included in the prompt to guide model generation. Example: `WithMaxSummaryWords(150)` requests summary within 150 words.
- **`WithPrompt(prompt string)`**: Provide custom summary prompt. Prompt must include placeholder `{conversation_text}` which will be replaced with conversation content. Optionally include `{max_summary_words}` for word limit instruction.

**Custom Prompt Example:**

```go
customPrompt := `Analyze the following conversation and provide a concise summary, focusing on key decisions, action items, and important context.
Please keep it within {max_summary_words} words.

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

**Complete Configuration Example:**

```go
summarizer := summary.NewSummarizer(
    summaryModel,
    // Single trigger conditions (as direct options for NewSummarizer)
    summary.WithEventThreshold(20),
    summary.WithTokenThreshold(4000),
    summary.WithTimeThreshold(5*time.Minute),
    
    // Composite conditions (any met - OR logic)
    summary.WithChecksAny(
        summary.CheckEventThreshold(50),     // Use Check* functions
        summary.CheckTimeThreshold(10*time.Minute),
    ),
    
    // Composite conditions (all met - AND logic)
    summary.WithChecksAll(
        summary.CheckEventThreshold(10),     // Use Check* functions
        summary.CheckTokenThreshold(2000),
    ),
    
    // Summary generation configuration
    summary.WithMaxSummaryWords(200),
    summary.WithPrompt(customPrompt),
)
```

**Trigger Condition Details:**

| Configuration Method | Trigger Logic | Description |
|---------------------|---------------|-------------|
| `WithEventThreshold(n)` | Event count **strictly greater than** n | `n=20` requires 21 events to trigger |
| `WithTokenThreshold(n)` | Total token count **strictly greater than** n (deduplicated by InvocationID) | `n=4000` requires 4001 tokens to trigger |
| `WithTimeThreshold(d)` | Time since last update **strictly greater than** d | `d=5min` requires 5 min 1 sec to trigger |
| `WithChecksAny(...)` | Trigger when any sub-condition is met | OR logic |
| `WithChecksAll(...)` | Trigger only when all sub-conditions are met | AND logic |

**Default Behavior:** If no trigger conditions are configured, summary functionality won't automatically trigger (requires manual forced invocation).

#### Incremental Summary Working Principle

The summary system uses an incremental processing mechanism to avoid redundant computation of already-summarized historical events:

**Processing Flow:**

1. **First Summary**: Process all historical events
   ```
   Input: [Event1, Event2, Event3, ..., Event20]
   Output: Summary1 (updated_at = Event20.timestamp)
   ```

2. **Incremental Summary**: Only process new events, with previous summary as context
   ```
   Input: [
       SystemEvent(content=Summary1),  // Previous summary as system message
       Event21,                        // New events
       Event22,
       ...,
       Event40
   ]
   Output: Summary2 (updated_at = Event40.timestamp)
   ```

3. **Time Filtering**: Use `updated_at` timestamp to avoid duplicate processing
   ```go
   // Only select events with timestamp strictly greater than last summary time
   func computeDeltaSince(sess *session.Session, since time.Time, filterKey string) ([]event.Event, time.Time) {
       for _, e := range sess.Events {
           if !since.IsZero() && !e.Timestamp.After(since) {
               continue  // Skip already-summarized events
           }
           // ...
       }
   }
   ```

**Core Advantages:**

- **Efficient Processing**: Only compute increments, avoid repeatedly analyzing same content
- **Context Preservation**: Previous summary prepended ensures continuity
- **Precise Timestamps**: `updated_at` records timestamp of last summarized event

#### Cascading Summary Mechanism

When processing branch summaries (non-empty filterKey), the framework automatically triggers complete session summary:

```go
// Pseudocode example
if filterKey != "" {
    // 1. Process branch summary
    SummarizeSession(ctx, summarizer, sess, filterKey, force)
    
    // 2. Automatically trigger complete session summary
    SummarizeSession(ctx, summarizer, sess, "", force)
}
```

**Use Cases:**

- **Multi-Agent Systems**: Each Agent maintains its own branch summary (filterKey = agentName)
- **Unified View**: Complete session summary (filterKey = "") provides global context

#### Summary Retrieval Priority

The framework provides intelligent summary retrieval strategy:

```go
// Priority 1: Complete session summary (filterKey="")
summary := sess.Summaries[""]

// Priority 2: Fallback to any non-empty branch summary
if summary == nil {
    for key, s := range sess.Summaries {
        if s != nil && s.Summary != "" {
            summary = s
            break
        }
    }
}
```

#### Session Service Options

Configure asynchronous summary processing in session service:

- **`WithSummarizer(s summary.SessionSummarizer)`**: Inject summarizer into session service.
- **`WithAsyncSummaryNum(num int)`**: Set number of async worker goroutines for summary processing. Default is 2. More workers allow higher concurrency but consume more resources.
- **`WithSummaryQueueSize(size int)`**: Set size of summary task queue. Default is 100. Larger queue allows more pending tasks but consumes more memory.
- **`WithSummaryJobTimeout(timeout time.Duration)`** _(Memory mode only)_: Set timeout for processing single summary task. Default is 30 seconds.

#### Retrieving Summaries

Get the latest summary text from a session:

```go
summaryText, found := sessionService.GetSessionSummaryText(ctx, sess)
if found {
    fmt.Printf("Summary: %s\n", summaryText)
}
```

#### How It Works

1. **Incremental Processing**: The summarizer tracks the last summary time for each session. In subsequent runs, it only processes events that occurred after the last summary.

2. **Incremental Summarization**: New events are combined with the previous summary (prepended as a system event) to generate an updated summary that includes both old context and new information.

3. **Trigger Condition Evaluation**: Before generating a summary, the summarizer evaluates configured trigger conditions (event count, token count, time threshold). If conditions aren't met and `force=false`, summary is skipped.

4. **Asynchronous Workers**: Summary tasks are distributed to multiple worker goroutines using a hash-based distribution strategy. This ensures tasks for the same session are processed sequentially, while different sessions can be processed in parallel.

5. **Fallback Mechanism**: If asynchronous enqueuing fails (queue full, context cancelled, or workers not initialized), the system automatically falls back to synchronous processing.

#### Best Practices

1. **Choose Appropriate Thresholds**: Set event/token thresholds based on LLM's context window and conversation patterns. For GPT-4 (8K context), consider using `WithTokenThreshold(4000)` to leave room for responses.

2. **Use Asynchronous Processing**: Always use `EnqueueSummaryJob` instead of `CreateSessionSummary` in production to avoid blocking conversation flow.

3. **Monitor Queue Size**: If you frequently see "queue is full" warnings, increase `WithSummaryQueueSize` or `WithAsyncSummaryNum`.

4. **Customize Prompts**: Tailor summary prompts to your application needs. For example, if you're building a customer support Agent, focus on key issues and resolutions.

5. **Balance Word Limits**: Set `WithMaxSummaryWords` to balance context retention with token usage reduction. Typical values range from 100-300 words.

6. **Test Trigger Conditions**: Experiment with different `WithChecksAny` and `WithChecksAll` combinations to find optimal balance between summary frequency and cost.

#### Performance Considerations

- **LLM Cost**: Each summary generation calls the LLM. Monitor trigger conditions to balance cost with context retention.
- **Memory Usage**: Summaries are stored alongside events. Configure appropriate TTL to manage memory in long-running sessions.
- **Asynchronous Workers**: More workers improve throughput but consume more resources. Start with 2-4 workers and scale based on load.
- **Queue Capacity**: Adjust queue size based on expected concurrency and summary generation time.

### Complete Example

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

    // Create LLM model
    llm, err := openai.NewModel(
        openai.WithAPIKey("your-api-key"),
        openai.WithModelName("gpt-4"),
    )
    if err != nil {
        // Handle error...
        return
    }

    // Create summarizer
    summarizer := summary.NewSummarizer(
        llm,
        summary.WithMaxSummaryWords(200),
        summary.WithChecksAny(
            summary.CheckEventThreshold(20),
            summary.CheckTokenThreshold(4000),
            summary.CheckTimeThreshold(5*time.Minute),
        ),
    )

    // Create session service
    sessionService := inmemory.NewSessionService(
        inmemory.WithSummarizer(summarizer),
        inmemory.WithAsyncSummaryNum(2),
        inmemory.WithSummaryQueueSize(100),
        inmemory.WithSummaryJobTimeout(30*time.Second),
    )

    // Create Agent
    agent := llmagent.New(
        "my-agent",
        llmagent.WithModel(llm),
        llmagent.WithAddSessionSummary(true),
    )

    // Create Runner
    r := runner.NewRunner("my-app", agent,
        runner.WithSessionService(sessionService))

    // Run conversation
    userMsg := model.NewUserMessage("Tell me about AI")
    eventChan, err := r.Run(ctx, "user123", "session456", userMsg)
    if err != nil {
        // Handle error...
        return
    }

    // Consume events
    for event := range eventChan {
        if event == nil || event.Response == nil {
            continue
        }
        if event.Response.Error != nil {
            // Handle error event...
            continue
        }
        if len(event.Response.Choices) > 0 {
            choice := event.Response.Choices[0]
            if choice.Delta.Content != "" {
                // Process streaming content...
            } else if choice.Message.Content != "" {
                // Process complete content...
            }
        }
        if event.IsFinalResponse() {
            break
        }
    }
}
```

## References

- [Session Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner)
- [Summary Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary)

By properly using session management in combination with session summary mechanisms, you can build stateful intelligent Agents that maintain conversation context while efficiently managing memory, providing users with continuous and personalized interaction experiences.
