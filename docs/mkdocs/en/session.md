# Session Management

## Overview

tRPC-Agent-Go provides powerful session (Session) management capabilities to maintain conversation history and context information during interactions between Agents and users. The session management module supports multiple storage backends, including in-memory storage and Redis storage, providing flexible state persistence for Agent applications.

### 🎯 Key Features

- **Session persistence**: Save complete conversation history and context.
- **Multiple storage backends**: Support in-memory storage and Redis storage.
- **Event tracking**: Fully record all interaction events within a session.
- **Multi-level storage**: Support application-level, user-level, and session-level data storage.
- **Concurrency safety**: Built-in read-write locks ensure safe concurrent access.
- **Automatic management**: After specifying the Session Service in Runner, sessions are automatically created, loaded, and updated.

## Core Concepts

### Session Hierarchy

```
Application
├── User Sessions
│   ├── Session 1
│   │   ├── Session Data
│   │   └── Events
│   └── Session 2
│       ├── Session Data
│       └── Events
└── App Data
```

### Data Levels

- **App Data**: Global shared data, such as system configuration and feature flags.
- **User Data**: User-level data shared across all sessions of the same user, such as user preferences.
- **Session Data**: Session-level data storing the context and state of a single conversation.

## Usage Examples

### Integrate Session Service

Use `runner.WithSessionService` to provide complete session management for the Agent runner. If not specified, in-memory session management is used by default. Runner automatically handles session creation, loading, and updates, so users do not need additional operations or care about internal details:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

// Choose session service type.
var sessionService session.Service

// Option 1: Use in-memory storage (development/testing).
sessionService = inmemory.NewSessionService()

// Option 2: Use Redis storage (production).
sessionService, err = redis.NewService(
    redis.WithRedisClientURL("redis://your-username:yourt-password@127.0.0.1:6379"),
)

// Create Runner and configure session service.
runner := runner.NewRunner(
    "my-agent",
    llmAgent,
    runner.WithSessionService(sessionService), // Key configuration.
)

// Use Runner for multi-turn conversation.
eventChan, err := runner.Run(ctx, userID, sessionID, userMessage)
```

After integrating session management, the Agent gains automatic session capabilities, including:

1. **Automatic session persistence**: Each AI interaction is automatically saved to the session.
2. **Context continuity**: Automatically load historical conversation context to enable true multi-turn conversations.
3. **State management**: Maintain three levels of state data: application, user, and session.
4. **Event stream processing**: Automatically record all interaction events such as user input, AI responses, and tool calls.

### Basic Session Operations

If you need to manually manage existing sessions (e.g., to query statistics of existing Sessions), you can use the APIs provided by the Session Service.

#### Create and Manage Sessions

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "trpc.group/trpc-go/trpc-agent-go/session"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/event"
)

func main() {
    // Create in-memory session service.
    sessionService := inmemory.NewSessionService()

    // Create session.
    key := session.Key{
        AppName:   "my-agent",
        UserID:    "user123",
        SessionID: "", // Empty string will auto-generate a UUID.
    }

    initialState := session.StateMap{
        "language": []byte("en-US"),
        "theme":    []byte("dark"),
    }

    createdSession, err := sessionService.CreateSession(
        context.Background(),
        key,
        initialState,
    )
    if err != nil {
        panic(err)
    }

    fmt.Printf("Created session: %s\n", createdSession.ID)
}
```

#### GetSession

```go
// GetSession retrieves a specified session by key.
func (s *SessionService) GetSession(
    ctx context.Context,
    key session.Key,
    options ...session.Option,
) (*Session, error)
```

- **Function**: Retrieve an existing session based on AppName, UserID, and SessionID.
- **Params**:
  - `key`: Session key, must include complete AppName, UserID, and SessionID.
  - `options`: Optional parameters, such as `session.WithEventNum(10)` to limit the number of returned events.
- **Returns**:
  - If the session does not exist, returns `nil, nil`.
  - If the session exists, returns the complete session object (including merged app, user, and session state).

Usage:

```go
// Retrieve full session.
session, err := sessionService.GetSession(ctx, session.Key{
    AppName:   "my-agent",
    UserID:    "user123",
    SessionID: "session-id-123",
})

// Retrieve session with only the latest 10 events.
session, err := sessionService.GetSession(ctx, key,
    session.WithEventNum(10))

// Retrieve events after a specified time.
session, err := sessionService.GetSession(ctx, key,
    session.WithEventTime(time.Now().Add(-1*time.Hour)))
```

#### DeleteSession

```go
// DeleteSession removes the specified session.
func (s *SessionService) DeleteSession(
    ctx context.Context,
    key session.Key,
    options ...session.Option,
) error
```

- **Function**: Remove the specified session from storage. If the user has no other sessions, the user record is automatically cleaned up.
- **Characteristics**:
  - Deleting a non-existent session does not produce an error.
  - Automatically cleans up empty user-session mappings.
  - Thread-safe operations.

Usage:

```go
// Delete specified session.
err := sessionService.DeleteSession(ctx, session.Key{
    AppName:   "my-agent",
    UserID:    "user123",
    SessionID: "session-id-123",
})
if err != nil {
    log.Printf("Failed to delete session: %v", err)
}
```

#### ListSessions

```go
// List all sessions of a user.
sessions, err := sessionService.ListSessions(
    context.Background(),
    session.UserKey{
        AppName: "my-agent",
        UserID:  "user123",
    },
)
```

#### State Management

```go
// Update app state.
appState := session.StateMap{
    "version": []byte("1.0.0"),
    "config":  []byte(`{"feature_flags": {"new_ui": true}}`),
}
err := sessionService.UpdateAppState(context.Background(), "my-agent", appState)

// Update user state.
userKey := session.UserKey{
    AppName: "my-agent",
    UserID:  "user123",
}
userState := session.StateMap{
    "preferences": []byte(`{"notifications": true}`),
    "profile":     []byte(`{"name": "Alice"}`),
}
err = sessionService.UpdateUserState(context.Background(), userKey, userState)

// Get session (including merged state).
retrievedSession, err = sessionService.GetSession(
    context.Background(),
    session.Key{
        AppName:   "my-agent",
        UserID:    "user123",
        SessionID: retrievedSession.ID,
    },
)
```

## Storage Backends

### In-memory Storage

Suitable for development environments and small-scale applications:

```go
import "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

// Create in-memory session service.
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(200), // Limit to at most 200 events per session.
)
```

#### In-memory Configuration Options

- **`WithSessionEventLimit(limit int)`**: Sets the maximum number of events stored per session. Default is 1000. When the limit is exceeded, older events are evicted.
- **`WithSessionTTL(ttl time.Duration)`**: Sets the TTL for session state and event list. Default is 0 (no expiration). If set to 0, sessions will not expire automatically.
- **`WithAppStateTTL(ttl time.Duration)`**: Sets the TTL for application-level state. Default is 0 (no expiration). If not set, app state will not expire automatically.
- **`WithUserStateTTL(ttl time.Duration)`**: Sets the TTL for user-level state. Default is 0 (no expiration). If not set, user state will not expire automatically.
- **`WithCleanupInterval(interval time.Duration)`**: Sets the interval for automatic cleanup of expired data. Default is 0 (auto-determined). If set to 0, automatic cleanup will be determined based on TTL configuration. Default cleanup interval is 5 minutes if any TTL is configured.

**Example with full configuration:**

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(500),
    inmemory.WithSessionTTL(30*time.Minute),
    inmemory.WithAppStateTTL(24*time.Hour),
    inmemory.WithUserStateTTL(7*24*time.Hour),
    inmemory.WithCleanupInterval(10*time.Minute),
)

// Configuration effects:
// - Each session stores up to 500 events, automatically evicting oldest events when exceeded
// - Session data expires after 30 minutes of inactivity
// - Application-level state expires after 24 hours
// - User-level state expires after 7 days
// - Cleanup operation runs every 10 minutes to remove expired data
```

**Default configuration example:**

```go
// Create in-memory session service with default configuration
sessionService := inmemory.NewSessionService()

// Default configuration effects:
// - Each session stores up to 1000 events (default value)
// - All data never expires (TTL is 0)
// - No automatic cleanup (CleanupInterval is 0)
// - Suitable for development environments or short-running applications
```

### Redis Storage

Suitable for production environments and distributed applications:

```go
import "trpc.group/trpc-go/trpc-agent-go/session/redis"

// Create using Redis URL.
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://your-username:yourt-password@127.0.0.1:6379"),
    redis.WithSessionEventLimit(500),
)

// Or use a preconfigured Redis instance.
sessionService, err := redis.NewService(
    redis.WithInstanceName("my-redis-instance"),
)
```

#### Redis Configuration Options

- **`WithSessionEventLimit(limit int)`**: Sets the maximum number of events stored per session. Default is 1000. When the limit is exceeded, older events are evicted.
- **`WithRedisClientURL(url string)`**: Creates a Redis client from URL. Format: `redis://[username:password@]host:port[/database]`.
- **`WithRedisInstance(instanceName string)`**: Uses a preconfigured Redis instance from storage. Note: `WithRedisClientURL` has higher priority than `WithRedisInstance`.
- **`WithExtraOptions(extraOptions ...interface{})`**: Sets extra options for the Redis session service. This option is mainly used for customized Redis client builders and will be passed to the builder.
- **`WithSessionTTL(ttl time.Duration)`**: Sets the TTL for session state and event list. Default is 0 (no expiration). If set to 0, sessions will not expire.
- **`WithAppStateTTL(ttl time.Duration)`**: Sets the TTL for application-level state. Default is 0 (no expiration). If not set, app state will not expire.
- **`WithUserStateTTL(ttl time.Duration)`**: Sets the TTL for user-level state. Default is 0 (no expiration). If not set, user state will not expire.

**Example with full configuration:**

````go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379/0"),
    redis.WithSessionEventLimit(1000),
    redis.WithSessionTTL(30*time.Minute),
    redis.WithAppStateTTL(24*time.Hour),
    redis.WithUserStateTTL(7*24*time.Hour),
)

// Configuration effects:
// - Connects to local Redis server database 0
// - Each session stores up to 1000 events, automatically evicting oldest events when exceeded
// - Session data expires after 30 minutes of inactivity
// - Application-level state expires after 24 hours
// - User-level state expires after 7 days
// - Uses Redis TTL mechanism for automatic cleanup, no manual cleanup needed

**Default configuration example:**

```go
// Create Redis session service with default configuration (requires pre-configured Redis instance)
sessionService, err := redis.NewService()

// Default configuration effects:
// - Each session stores up to 1000 events (default value)
// - All data never expires (TTL is 0)
// - Requires pre-registered Redis instance via storage.RegisterRedisInstance
// - Suitable for scenarios requiring persistence but no automatic expiration
````

#### Configuration Reuse

If multiple components need Redis, you can configure a Redis instance and reuse the configuration across components.

```go
    redisURL := fmt.Sprintf("redis://%s", "127.0.0.1:6379")
    storage.RegisterRedisInstance("my-redis-instance", storage.WithClientBuilderURL(redisURL))
    sessionService, err = redis.NewService(redis.WithRedisInstance("my-redis-instance"))
```

#### Redis Storage Structure

```
# App data
appdata:{appName} -> Hash {key: value}

# User data
userdata:{appName}:{userID} -> Hash {key: value}

# Session data
session:{appName}:{userID} -> Hash {sessionID: SessionData(JSON)}

# Event records
events:{appName}:{userID}:{sessionID} -> SortedSet {score: timestamp, value: Event(JSON)}
```

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
summaryModel, err := openai.NewModel(
    openai.WithAPIKey("your-api-key"),
    openai.WithModelName("gpt-4"),
)
if err != nil {
    panic(err)
}

// Create summarizer with trigger conditions.
summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithEventThreshold(20),        // Trigger after 20 events.
    summary.WithTokenThreshold(4000),      // Trigger after 4000 tokens.
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
)

// Option 1: In-memory session service with summarizer.
sessionService := inmemory.NewSessionService(
    inmemory.WithSummarizer(summarizer),
    inmemory.WithAsyncSummaryNum(2),                // 2 async workers.
    inmemory.WithSummaryQueueSize(100),             // Queue size 100.
    inmemory.WithSummaryJobTimeout(30*time.Second), // 30s timeout per job.
)

// Option 2: Redis session service with summarizer.
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithSummarizer(summarizer),
    redis.WithAsyncSummaryNum(4),           // 4 async workers.
    redis.WithSummaryQueueSize(200),        // Queue size 200.
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
    llmagent.WithMaxHistoryRuns(10),        // Keep last 10 runs when summary exists.
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

- The session summary is prepended as a system message.
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
│ System Prompt                       │
├─────────────────────────────────────┤
│ Session Summary (system message)    │ ← Condensed history
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

- **`WithEventThreshold(eventCount int)`**: Trigger summarization when the number of events exceeds the threshold. Example: `WithEventThreshold(20)` triggers after 20 events.
- **`WithTokenThreshold(tokenCount int)`**: Trigger summarization when the total token count exceeds the threshold. Example: `WithTokenThreshold(4000)` triggers after 4000 tokens.
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
    summary.WithPrompt(customPrompt),
    summary.WithMaxSummaryWords(100),
    summary.WithEventThreshold(15),
)
```

#### Session Service Options

Configure async summary processing in session services:

- **`WithSummarizer(s summary.SessionSummarizer)`**: Inject the summarizer into the session service.
- **`WithAsyncSummaryNum(num int)`**: Set the number of async worker goroutines for summary processing. Default is 2. More workers allow higher concurrency but consume more resources.
- **`WithSummaryQueueSize(size int)`**: Set the size of the summary job queue. Default is 100. Larger queues allow more pending jobs but consume more memory.
- **`WithSummaryJobTimeout(timeout time.Duration)`** _(in-memory only)_: Set the timeout for processing a single summary job. Default is 30 seconds.

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
summaryText, found := sessionService.GetSessionSummaryText(ctx, sess)
if found {
    fmt.Printf("Summary: %s\n", summaryText)
}
```

### How It Works

1. **Incremental Processing**: The summarizer tracks the last summarization time for each session. On subsequent runs, it only processes events that occurred after the last summary.

2. **Delta Summarization**: New events are combined with the previous summary (prepended as a system event) to generate an updated summary that incorporates both old context and new information.

3. **Trigger Evaluation**: Before generating a summary, the summarizer evaluates configured trigger conditions (event count, token count, time threshold). If conditions aren't met and `force=false`, summarization is skipped.

4. **Async Workers**: Summary jobs are distributed across multiple worker goroutines using hash-based distribution. This ensures jobs for the same session are processed sequentially while different sessions can be processed in parallel.

5. **Fallback Mechanism**: If async enqueueing fails (queue full, context cancelled, or workers not initialized), the system automatically falls back to synchronous processing.

### Best Practices

1. **Choose appropriate thresholds**: Set event/token thresholds based on your LLM's context window and conversation patterns. For GPT-4 (8K context), consider `WithTokenThreshold(4000)` to leave room for responses.

2. **Use async processing**: Always use `EnqueueSummaryJob` instead of `CreateSessionSummary` in production to avoid blocking the conversation flow.

3. **Monitor queue sizes**: If you see frequent "queue is full" warnings, increase `WithSummaryQueueSize` or `WithAsyncSummaryNum`.

4. **Customize prompts**: Tailor the summarization prompt to your application's needs. For example, if you're building a customer support agent, focus on key issues and resolutions.

5. **Balance word limits**: Set `WithMaxSummaryWords` to balance between preserving context and reducing token usage. Typical values range from 100-300 words.

6. **Test trigger conditions**: Experiment with different combinations of `WithChecksAny` and `WithChecksAll` to find the right balance between summary frequency and cost.

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
    llm, _ := openai.NewModel(
        openai.WithAPIKey("your-api-key"),
        openai.WithModelName("gpt-4"),
    )

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
        inmemory.WithSummaryJobTimeout(30*time.Second),
    )

    // Create agent with summary injection enabled.
    agent := llmagent.New(
        "my-agent",
        llmagent.WithModel(llm),
        llmagent.WithAddSessionSummary(true),
        llmagent.WithMaxHistoryRuns(10),
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
