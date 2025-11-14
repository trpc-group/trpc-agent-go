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
    )

    // 5. Create Runner and inject Session Service
    r := runner.NewRunner(
        "my-agent",
        agent,
        runner.WithSessionService(sessionService),
    )

    // 6. First conversation
    userMsg1 := model.NewUserMessage("My name is Alice")
    eventChan, _ := r.Run(ctx, "user123", "session-001", userMsg1)
    fmt.Print("AI: ")
    for event := range eventChan {
        if len(event.Response.Choices) > 0 {
            // Streaming output, use Delta.Content
            fmt.Print(event.Response.Choices[0].Delta.Content)
        }
    }
    fmt.Println()

    // 7. Second conversation - automatically load history, AI remembers user's name
    userMsg2 := model.NewUserMessage("What's my name?")
    eventChan, _ = r.Run(ctx, "user123", "session-001", userMsg2)
    fmt.Print("AI: ")
    for event := range eventChan {
        if len(event.Response.Choices) > 0 {
            // Streaming output, use Delta.Content
            fmt.Print(event.Response.Choices[0].Delta.Content)
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
│ Session Summary (system message)        │ ← Compressed history
├─────────────────────────────────────────┤
│ Event 1 (after summary)                 │ ┐
│ Event 2                                 │ │
│ Event 3                                 │ │ New events
│ ...                                     │ │ (fully retained)
│ Event N (current message)               │ ┘
└─────────────────────────────────────────┘
```

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

| Scenario | Recommended Value | Description |
| -------- | ----------------- | ----------- |
| Short-term conversations | 100-200 | Customer service, single tasks |
| Medium-term sessions | 500-1000 | Daily assistant, multi-turn collaboration |
| Long-term sessions | 1000-2000 | Personal assistant, ongoing projects (use with summary) |
| Debug/testing | 50-100 | Quick validation, reduce noise |

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

| Storage Type | Expiration Mechanism | Auto Cleanup |
| ------------ | -------------------- | ------------ |
| Memory | Periodic scanning + access-time checking | Yes |
| Redis | Redis native TTL | Yes |
| PostgreSQL | Periodic scanning (soft delete or hard delete) | Yes |
| MySQL | Periodic scanning (soft delete or hard delete) | Yes |

## Storage Backend Comparison

tRPC-Agent-Go provides four session storage backends to meet different scenario requirements:

| Storage Type | Use Case | Advantages | Disadvantages |
| ------------ | -------- | ---------- | ------------- |
| Memory | Development/testing, small-scale | Simple and fast, no external dependencies | Data not persistent, no distributed support |
| Redis | Production, distributed | High performance, distributed support, auto-expiration | Requires Redis service |
| PostgreSQL | Production, complex queries | Relational database, supports complex queries, JSONB | Relatively heavy, requires database |
| MySQL | Production, complex queries | Widely used, supports complex queries, JSON | Relatively heavy, requires database |

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

| Schema | Prefix | Final Table Name |
|--------|--------|------------------|
| (none) | (none) | `session_states` |
| (none) | `app1_` | `app1_session_states` |
| `my_schema` | (none) | `my_schema.session_states` |
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

| Configuration | Delete Operation | Query Behavior | Data Recovery |
|---------------|------------------|----------------|---------------|
| `softDelete=true` | `UPDATE SET deleted_at = NOW()` | Filter `deleted_at IS NULL` | Recoverable |
| `softDelete=false` | `DELETE FROM ...` | Query all records | Not recoverable |

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

- **`WithHost(host string)`**: MySQL server address. Default is `localhost`.
- **`WithPort(port int)`**: MySQL server port. Default is `3306`.
- **`WithUser(user string)`**: Database username. Default is `root`.
- **`WithPassword(password string)`**: Database password. Default is empty string.
- **`WithDatabase(database string)`**: Database name. Default is `trpc_sessions`.
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
    mysql.WithHost("localhost"),
    mysql.WithPassword("your-password"),
)
// Effect:
// - Connect to localhost:3306, database trpc_sessions
// - Each session max 1000 events
// - Data never expires
// - 2 async persistence workers

// Complete production environment configuration
sessionService, err := mysql.NewService(
    // Connection configuration
    mysql.WithHost("localhost"),
    mysql.WithPort(3306),
    mysql.WithUser("root"),
    mysql.WithPassword("your-password"),
    mysql.WithDatabase("trpc_sessions"),
    
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
    mysql.WithHost("localhost"),
    mysql.WithTablePrefix("app1_"),  // Table name: app1_session_states
)
```

### Soft Delete and TTL Cleanup

**Soft Delete Configuration:**

```go
// Enable soft delete (default)
sessionService, err := mysql.NewService(
    mysql.WithHost("localhost"),
    mysql.WithSoftDelete(true),
)

// Disable soft delete (physical delete)
sessionService, err := mysql.NewService(
    mysql.WithHost("localhost"),
    mysql.WithSoftDelete(false),
)
```

**Delete Behavior Comparison:**

| Configuration | Delete Operation | Query Behavior | Data Recovery |
|---------------|------------------|----------------|---------------|
| `softDelete=true` | `UPDATE SET deleted_at = NOW()` | Filter `deleted_at IS NULL` | Recoverable |
| `softDelete=false` | `DELETE FROM ...` | Query all records | Not recoverable |

**TTL Auto Cleanup:**

```go
sessionService, err := mysql.NewService(
    mysql.WithHost("localhost"),
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
    mysql.WithHost("localhost"),
    mysql.WithPassword("your-password"),
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
    mysql.WithHost("localhost"),
    mysql.WithPassword("your-password"),
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

**Automatic Triggering (Recommended):** Runner automatically checks trigger conditions after each conversation completes, generating summaries asynchronously in background when conditions are met.

**Manual Triggering:**

```go
// Asynchronous summarization (recommended) - background processing, non-blocking
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    false, // force=false, respects trigger conditions
)

// Synchronous summarization - immediate processing, blocking
err := sessionService.CreateSessionSummary(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    false,
)

// Force summarization - bypass trigger conditions
err := sessionService.EnqueueSummaryJob(
    ctx,
    sess,
    session.SummaryFilterKeyAllContents,
    true, // force=true, bypass all trigger condition checks
)
```

### Context Injection Mechanism

**Mode 1: Enable Summary Injection (Recommended)**

```go
llmagent.WithAddSessionSummary(true)
```

- Summary automatically prepended as system message to LLM input
- Includes all incremental events after summary timestamp
- Ensures complete context: condensed history + complete new conversations

**Mode 2: Without Summary**

```go
llmagent.WithAddSessionSummary(false)
llmagent.WithMaxHistoryRuns(10)  // Include only last 10 conversation rounds
```

### Advanced Summarizer Options

```go
summarizer := summary.NewSummarizer(
    summaryModel,
    // Trigger conditions
    summary.WithEventThreshold(20),
    summary.WithTokenThreshold(4000),
    summary.WithTimeThreshold(5*time.Minute),
    
    // Composite conditions (any met)
    summary.WithChecksAny(
        summary.CheckEventThreshold(50),
        summary.CheckTimeThreshold(10*time.Minute),
    ),
    
    // Composite conditions (all met)
    summary.WithChecksAll(
        summary.CheckEventThreshold(10),
        summary.CheckTokenThreshold(2000),
    ),
    
    // Summary generation
    summary.WithMaxSummaryWords(200),
    summary.WithPrompt(customPrompt),
)
```

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
    llm, _ := openai.NewModel(
        openai.WithAPIKey("your-api-key"),
        openai.WithModelName("gpt-4"),
    )

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
    eventChan, _ := r.Run(ctx, "user123", "session456", userMsg)

    // Consume events
    for event := range eventChan {
        // Process events...
    }
}
```

## References

- [Session Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner)
- [Summary Example](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary)

By properly using session management in combination with session summary mechanisms, you can build stateful intelligent Agents that maintain conversation context while efficiently managing memory, providing users with continuous and personalized interaction experiences.
