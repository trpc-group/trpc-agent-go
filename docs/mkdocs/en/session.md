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

```go
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
```

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

## References

- Reference example.

By properly using session management, you can build stateful intelligent Agents that provide continuous and personalized interaction experiences for users.
