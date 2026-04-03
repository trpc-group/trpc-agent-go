# Session Management

## Overview

The tRPC-Agent-Go framework provides powerful session management capabilities for maintaining conversation history and context during Agent-user interactions. With automatic persistence of conversation records, intelligent summary compression, and flexible storage backends, session management provides a complete infrastructure for building stateful intelligent Agents.

### Purpose

Session manages the context of the current conversation, isolated by `<appName, userID, SessionID>`. It stores user messages, Agent replies, tool call results, and summaries generated from these contents, supporting multi-turn conversation scenarios.

Within the same conversation, it enables natural continuity between turns, preventing users from having to re-describe the same problem or provide the same parameters in each turn.

### Key Features

- **Context Management**: Automatically loads conversation history for true multi-turn conversations
- **Session Summary**: Uses LLM to automatically compress long conversation history, significantly reducing token consumption while preserving key context
- **Event Limit**: Controls the maximum number of events stored per session to prevent memory overflow
- **Event Pagination**: PostgreSQL/MySQL support paged history reads for `GetSession`
- **TTL Management**: Supports automatic expiration and cleanup of session data
- **Multiple Storage Backends**: Supports Memory, SQLite, Redis, PostgreSQL, PGVector, MySQL, and ClickHouse
- **Concurrency Safe**: Built-in read-write locks ensure safe concurrent access
- **Automatic Management**: Automatically handles session creation, loading, and updates when integrated with Runner
- **Soft Delete Support**: SQLite/PostgreSQL/PGVector/MySQL/ClickHouse support soft delete for data recovery

## Quick Start

### Integration with Runner

Session management in tRPC-Agent-Go is integrated into the Runner via `runner.WithSessionService`. The Runner automatically handles session creation, loading, updating, and persistence.

**Supported Storage Backends:** Memory, SQLite, Redis, PostgreSQL, PGVector, MySQL, ClickHouse

**Default Behavior:** If `runner.WithSessionService` is not configured, the Runner defaults to in-memory storage, and data will be lost after process restart.

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
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

func main() {
    // 1. Create LLM model
    llm := openai.New("gpt-4", openai.WithAPIKey("your-api-key"))

    // 2. (Optional) Create summarizer - automatically compresses long conversation history
    summarizer := summary.NewSummarizer(
        llm,
        summary.WithChecksAny(
            summary.CheckEventThreshold(20),
            summary.CheckTokenThreshold(4000),
            summary.CheckTimeThreshold(5*time.Minute),
        ),
        summary.WithMaxSummaryWords(200),
    )

    // 3. Create Session Service (optional, defaults to in-memory storage)
    sessionService := inmemory.NewSessionService(
        inmemory.WithSummarizer(summarizer),
        inmemory.WithAsyncSummaryNum(2),
        inmemory.WithSummaryQueueSize(100),
    )

    // 4. Create Agent
    agent := llmagent.New(
        "my-agent",
        llmagent.WithModel(llm),
        llmagent.WithInstruction("You are a helpful assistant"),
        llmagent.WithAddSessionSummary(true),
        // Optional: compact oversized historical tool results before the LLM call
        // WithAddSessionSummary(true) additionally enables one sync summary retry when needed
        llmagent.WithEnableContextCompaction(true),
        llmagent.WithContextCompactionToolResultMaxTokens(1024),  // old tool results → placeholder
        llmagent.WithContextCompactionOversizedToolResultMaxTokens(8192),  // any huge result → head+tail truncation
        llmagent.WithContextCompactionKeepRecentRequests(1),
        // Note: WithAddSessionSummary(true) ignores WithMaxHistoryRuns
        // Summary includes all history, incremental events are fully retained
    )

    // 5. Create Runner and inject Session Service
    r := runner.NewRunner(
        "my-agent",
        agent,
        runner.WithSessionService(sessionService),
    )

    // 6. First conversation
    ctx := context.Background()
    userMsg1 := model.NewUserMessage("My name is John")
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

    // 7. Second conversation - automatically loads history, AI remembers the user's name
    userMsg2 := model.NewUserMessage("What is my name?")
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
    fmt.Println() // Output: Your name is John
}
```

### Capabilities Provided by Runner

After integrating Session Service, the Runner automatically provides the following capabilities **without manually calling any Session API**:

1. **Automatic Session Creation**: Automatically creates a session on first conversation (generates UUID if SessionID is empty)
2. **Automatic Session Loading**: Automatically loads historical context at the start of each conversation
3. **Automatic Session Update**: Automatically saves new events after conversation ends
4. **Context Continuity**: Automatically injects conversation history into LLM input for multi-turn conversations
5. **Automatic Summary Generation** (optional): Generates summaries asynchronously in the background when trigger conditions are met

## Core Concepts

### Session Structure

Session is the core data structure for session management:

| Field | Type | Description |
| --- | --- | --- |
| `ID` | `string` | Session ID |
| `AppName` | `string` | Application name |
| `UserID` | `string` | User ID |
| `State` | `StateMap` | Session state (key-value pairs) |
| `Events` | `[]event.Event` | Session event list |
| `Tracks` | `map[Track]*TrackEvents` | Track event mapping |
| `Summaries` | `map[string]*Summary` | Session summary mapping |
| `UpdatedAt` | `time.Time` | Last update time |
| `CreatedAt` | `time.Time` | Creation time |

### Key Structure

A Session is uniquely identified by the `Key` structure:

```go
type Key struct {
    AppName   string // app name
    UserID    string // user id
    SessionID string // session id
}
```

### Service Interface

All storage backends implement the `session.Service` interface:

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

## Core Capabilities

### 1. Context Management

The core function of session management is maintaining conversation context, ensuring the Agent can remember historical interactions and respond intelligently based on history.

**How it works:**

- Automatically saves user input and AI responses for each turn
- Automatically loads historical events at the start of a new conversation
- Runner automatically injects historical context into LLM input

**Default behavior:** After Runner integration, context management is fully automated with no manual intervention required.

### 2. Event Limit

Controls the maximum number of events stored per session to prevent memory overflow from long conversations.

**Mechanism:**

- Automatically evicts oldest events when limit is exceeded (FIFO)
- Only affects storage, not business logic
- Applies to all storage backends

**Configuration example:**

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(500),
)
```

**Recommended configuration:**

| Scenario | Recommended Value | Description |
| --- | --- | --- |
| Short conversations | 100-200 | Customer support, single tasks |
| Medium sessions | 500-1000 | Daily assistant, multi-turn collaboration |
| Long sessions | 1000-2000 | Personal assistant, ongoing projects (use with summary) |
| Debug/Test | 50-100 | Quick validation, reduce noise |

### 3. TTL Management (Auto-Expiration)

Supports setting Time To Live for session data with automatic cleanup of expired data.

**Supported TTL types:**

- **SessionTTL**: Expiration time for session state and events
- **AppStateTTL**: Expiration time for app-level state
- **UserStateTTL**: Expiration time for user-level state

**Configuration example:**

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionTTL(30*time.Minute),
    inmemory.WithAppStateTTL(24*time.Hour),
    inmemory.WithUserStateTTL(7*24*time.Hour),
)
```

**TTL refresh behavior:**

TTL is only refreshed on **write operations** (e.g., CreateSession, AppendEvent, UpdateSessionState). Read operations (GetSession) do **not** refresh TTL.

**Expiration behavior:**

| Storage Type | Expiration Mechanism | Auto Cleanup |
| --- | --- | --- |
| Memory | Periodic scan + check on access | Yes |
| SQLite | Periodic scan (soft/hard delete) | Yes |
| Redis | Native Redis TTL | Yes |
| PostgreSQL | Periodic scan (soft/hard delete) | Yes |
| PGVector | Periodic scan (soft/hard delete) | Yes |
| MySQL | Periodic scan (soft/hard delete) | Yes |
| ClickHouse | Application-level cleanup + Native TTL | Yes |

## Storage Backend Comparison

tRPC-Agent-Go provides seven session storage backends for different scenarios:

| Storage Type | Use Case | Persistence | Distributed | Complex Queries |
| --- | --- | --- | --- | --- |
| [Memory](inmemory.md) | Dev/Test, small scale | ❌ | ❌ | ❌ |
| [SQLite](sqlite.md) | Local persistence, single-node | ✅ | ❌ | ✅ |
| [Redis](redis.md) | Production, distributed | ✅ | ✅ | ❌ |
| [PostgreSQL](postgres.md) | Production, complex queries | ✅ | ✅ | ✅ |
| [PGVector](pgvector.md) | Production, semantic recall | ✅ | ✅ | ✅ |
| [MySQL](mysql.md) | Production, complex queries | ✅ | ✅ | ✅ |
| [ClickHouse](clickhouse.md) | Production, massive data | ✅ | ✅ | ✅ |

## Hook Capabilities

Session Service supports a Hook mechanism for intercepting and modifying event writes and session reads.

### AppendEventHook

Intercept/modify/abort before event write. Useful for content safety, audit tagging, or blocking storage.

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

Intercept/modify/filter after session read. Useful for removing events with specific tags or dynamically supplementing Session state.

```go
type GetSessionContext struct {
    Context context.Context
    Key     Key
    Options *Options
}

type GetSessionHook func(ctx *GetSessionContext, next func() (*Session, error)) (*Session, error)
```

### Usage Example

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
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
        sess.Events = filterEvents(sess.Events)
        return sess, nil
    }),
)
```

**Chain of Responsibility**: Hooks form a chain via `next()`. You can return early to short-circuit subsequent logic, and errors propagate upward.

**Cross-Backend Consistency**: All storage backends (Memory, SQLite, Redis, PostgreSQL, PGVector, MySQL, ClickHouse) have unified Hook support. Simply inject Hook slices when constructing the service — the usage is identical across all backends.

## Advanced Usage

### Using Session Service API Directly

In most cases, you should use session management through the Runner, which handles all details automatically. However, in some special scenarios (such as session management dashboards, data migration, analytics, etc.), you may need to operate the Session Service directly.

#### List Sessions

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
// Fetch session metadata only, without Events or Tracks
sessions, err := sessionService.ListSessions(ctx, session.UserKey{
    AppName: "my-agent",
    UserID:  "user123",
}, session.WithListSessionOnlyMeta())
```

Notes:

- `session.WithListSessionOnlyMeta()` is only for `ListSessions`
- This optimization is currently supported only by the `inmemory` and `redis` backends

#### Delete Session

```go
err := sessionService.DeleteSession(ctx, session.Key{
    AppName:   "my-agent",
    UserID:    "user123",
    SessionID: "session-id-123",
})
```

#### Get Session Details

```go
// Get full session
sess, err := sessionService.GetSession(ctx, session.Key{
    AppName:   "my-agent",
    UserID:    "user123",
    SessionID: "session-id-123",
})

// Get session with last 10 events
sess, err := sessionService.GetSession(ctx, key,
    session.WithEventNum(10))

// Get events after a specific time
sess, err := sessionService.GetSession(ctx, key,
    session.WithEventTime(time.Now().Add(-1*time.Hour)))

// Get paged history events
// Supported only by PostgreSQL / MySQL GetSession
// EventPage cannot be combined with EventNum / EventTime
sess, err := sessionService.GetSession(ctx, key,
    session.WithGetSessionEventPage(20, 10))
```

#### Append Events to Session Directly

In some scenarios, you may need to append events to a session directly without calling the model. This is useful for:

- Pre-loading conversation history from external sources
- Inserting system messages or context before the first user query
- Recording user actions or metadata as events
- Programmatically building conversation context

**Important**: An Event can represent either a user request or a model response. When you use `Runner.Run()`, the framework automatically creates events for user messages and assistant replies.

**Example: Append User Message**

```go
import (
    "context"
    "github.com/google/uuid"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/session"
)

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

message := model.NewUserMessage("Hello, I'm learning Go programming.")

invocationID := uuid.New().String()
evt := event.NewResponseEvent(
    invocationID,
    "user",
    &model.Response{
        Done: false,
        Choices: []model.Choice{
            {
                Index:   0,
                Message: message,
            },
        },
    },
)
evt.RequestID = uuid.New().String()

if err := sessionService.AppendEvent(ctx, sess, evt); err != nil {
    return fmt.Errorf("append event failed: %w", err)
}
```

**Example: Append System Message**

```go
systemMessage := model.Message{
    Role:    model.RoleSystem,
    Content: "You are a Go programming assistant.",
}

evt := event.NewResponseEvent(
    uuid.New().String(),
    "system",
    &model.Response{
        Done:    false,
        Choices: []model.Choice{{Index: 0, Message: systemMessage}},
    },
)

if err := sessionService.AppendEvent(ctx, sess, evt); err != nil {
    return err
}
```

**Example: Append Assistant Message**

```go
assistantMessage := model.Message{
    Role:    model.RoleAssistant,
    Content: "Go is a statically typed, compiled programming language.",
}

evt := event.NewResponseEvent(
    uuid.New().String(),
    "assistant",
    &model.Response{
        Done:    false,
        Choices: []model.Choice{{Index: 0, Message: assistantMessage}},
    },
)

if err := sessionService.AppendEvent(ctx, sess, evt); err != nil {
    return err
}
```

**Required Event Fields**

When creating events with `event.NewResponseEvent()`, the following fields are required:

1. **Function parameters**:
   - `invocationID` (string): Unique identifier, typically `uuid.New().String()`
   - `author` (string): Event author (`"user"`, `"system"`, or agent name)
   - `response` (*model.Response): Response object containing Choices

2. **Response fields**:
   - `Choices` ([]model.Choice): At least one Choice with `Index` and `Message`
   - `Message`: Must contain `Content` or `ContentParts`

3. **Auto-generated fields** (set by `event.NewResponseEvent()`):
   - `ID`: Auto-generated UUID
   - `Timestamp`: Auto-set to current time
   - `Version`: Auto-set to `CurrentVersion`

4. **Persistence requirements**:
   - `Response != nil`
   - `!IsPartial` (or contains `StateDelta`)
   - `IsValidContent()` returns `true`

**Working with Runner**

When you subsequently use `Runner.Run()` on the same session:

1. Runner automatically loads the session (including all appended events)
2. Converts session events to messages
3. Includes all messages (appended + current) in the conversation context
4. Sends them together to the model

All appended events become part of the conversation history and are available to the model in subsequent interactions.

**Example**: See `examples/session/appendevent` ([code](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/appendevent))

## Track Events

Track events are a trajectory storage mechanism in Session that is independent of the main conversation events. They are primarily used for event storage in AGUI scenarios, allowing specific types of events to be recorded in a session without affecting the main conversation flow.

**Interface**:

The Track event API is defined on the `session.TrackService` interface, which is separate from `session.Service`:

```go
type TrackService interface {
    AppendTrackEvent(ctx context.Context, sess *Session, event *TrackEvent, opts ...Option) error
}
```

Not all storage backends implement `TrackService`. A type assertion is required:

| Storage Backend | Implements TrackService |
| --- | --- |
| Memory (inmemory) | ✅ |
| SQLite | ✅ |
| Redis | ✅ |
| PostgreSQL | ✅ |
| PGVector | ✅ |
| MySQL | ✅ |
| ClickHouse | ❌ |

**Basic usage**:

```go
// Obtain TrackService via type assertion
trackService, ok := sessionService.(session.TrackService)
if !ok {
    log.Fatal("current storage backend does not support TrackService")
}

// Append a track event
payload, _ := json.Marshal(map[string]any{"action": "button_click"})
err := trackService.AppendTrackEvent(ctx, sess, &session.TrackEvent{
    Track:     "ui-events",
    Payload:   payload,
    Timestamp: time.Now(),
})

// Retrieve track events from session
trackEvents, err := sess.GetTrackEvents("ui-events")
```

## Semantic Recall (PGVector Only)

The `session/pgvector` backend also implements `session.SearchableService`, so you can search a user's historical messages across one or more sessions by semantic similarity. Only persisted user/assistant text events are indexed; tool calls, tool results, partial events, and empty content are skipped.

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

If you are using LLMAgent, searchable backends such as `session/pgvector` can
also preload cross-session recall into the system prompt via
`llmagent.WithPreloadSessionRecall(...)`.

See [PGVector Session](pgvector.md) for configuration details, indexing behavior, and search filters.

## Related Documentation

- [Session Summary](summary.md) - Automatic compression of long conversation history
- [Memory Storage](inmemory.md) - Development and testing environment
- [SQLite Storage](sqlite.md) - Local persistence, single-node
- [Redis Storage](redis.md) - Production distributed storage
- [PostgreSQL Storage](postgres.md) - Relational database storage
- [PGVector Session](pgvector.md) - PostgreSQL session storage with semantic recall
- [MySQL Storage](mysql.md) - Relational database storage
- [ClickHouse Storage](clickhouse.md) - Massive data storage

## References

- [Session Examples](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/runner)
- [Summary Examples](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/summary)
- [Hook Examples](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/hook)
