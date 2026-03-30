# Redis Storage

Redis storage is suitable for production environments and distributed applications, providing high performance and automatic expiration capabilities.

## Features

- Redis-based persistence for sessions, events, and state
- Supports Redis Standalone / Sentinel / Cluster deployment modes
- Independent TTL control for Session, AppState, and UserState
- Optional async persistence to reduce write latency
- Optional OpenTelemetry tracing
- Async session summary generation
- AppendEvent / GetSession hook extension points

## Configuration Options

**Connection:**

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithRedisClientURL(url string)` | `string` | - | Create Redis client via URL, format: `redis://[username:password@]host:port[/database]` |
| `WithRedisInstance(instanceName string)` | `string` | - | Use a pre-configured Redis instance (lower priority than URL) |
| `WithExtraOptions(extraOptions ...any)` | `[]any` | `nil` | Extra options for the Redis client, passed to the underlying client builder |

**Session:**

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | Maximum events per session |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | TTL for session state and events; negative values are treated as 0 |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | TTL for app-level state |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | TTL for user-level state |
| `WithKeyPrefix(prefix string)` | `string` | `""` | Redis key prefix; all keys will start with `prefix:`. Useful when multiple apps share one Redis instance |
| `WithCompatMode(mode CompatMode)` | `CompatMode` | `CompatModeLegacy` | Storage compatibility mode. Options: `CompatModeNone`, `CompatModeLegacy`, `CompatModeTransition`. See [Storage Compatibility Mode (CompatMode)](#storage-compatibility-mode-compatmode) |
| `WithEnableUserSessionIndex(enable bool)` | `bool` | `false` | Enable the per-user session index for HashIdx. See [User Session Index](#user-session-index) |

**Async Persistence:**

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithEnableAsyncPersist(enable bool)` | `bool` | `false` | Enable async persistence. When enabled, `AppendEvent` and `AppendTrackEvent` write events to an internal channel and background workers flush them to Redis asynchronously |
| `WithAsyncPersisterNum(num int)` | `int` | `10` | Number of async persistence workers. Each worker handles one Event channel and one TrackEvent channel with a buffer size of 100 |

**Summary:**

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | Inject session summarizer. When not set, summary operations are no-ops |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | Number of summary processing workers |
| `WithSummaryQueueSize(size int)` | `int` | `100` | Summary task queue size |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | Timeout for a single summary job |

**Tracing:**

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithEnableTracing(enable bool)` | `bool` | `false` | Enable OpenTelemetry tracing. When enabled, operations such as `CreateSession`, `GetSession`, `AppendEvent`, `DeleteSession`, `AppendTrackEvent`, `CreateSessionSummary`, and `GetSessionSummaryText` automatically create spans |

!!! note "About Root Span"
    Session operations are executed by the Runner before and after the Agent's `Run()` call. The Agent's root span is created inside `agent.Run()`, so Session spans are not automatically attached under it. To see the full Session span chain in observability platforms like Langfuse, manually create a root span before calling `runner.Run()`:

    ```go
    import atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"

    // Create a root span before runner.Run(), so that session spans
    // (create_session, get_session, append_event, etc.) become children
    // of this root span via context propagation.
    ctx, span := atrace.Tracer.Start(ctx, "my_request")
    defer span.End()

    eventChan, err := r.Run(ctx, userID, sessionID, message)
    ```

**Hooks:**

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithAppendEventHook(hooks ...session.AppendEventHook)` | `[]session.AppendEventHook` | `nil` | Add event write hooks |
| `WithGetSessionHook(hooks ...session.GetSessionHook)` | `[]session.GetSessionHook` | `nil` | Add session read hooks |

## Basic Configuration

```go
import "trpc.group/trpc-go/trpc-agent-go/session/redis"

// Create via URL (recommended)
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://username:password@127.0.0.1:6379/0"),
    redis.WithSessionEventLimit(500),
)

// Full production configuration
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379/0"),
    redis.WithSessionEventLimit(1000),
    redis.WithSessionTTL(30*time.Minute),
    redis.WithAppStateTTL(24*time.Hour),
    redis.WithUserStateTTL(7*24*time.Hour),
)
// Effect:
// - Connect to local Redis database 0
// - Up to 1000 events per session
// - Sessions expire after 30 minutes of inactivity (Redis TTL)
// - App state expires after 24 hours
// - User state expires after 7 days
// - Uses Redis native TTL mechanism, no manual cleanup needed
```

## Instance Reuse

If multiple components need to use the same Redis instance, register and reuse it:

```go
import (
    redisstorage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
    "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

// Register Redis instance
redisURL := "redis://127.0.0.1:6379"
redisstorage.RegisterRedisInstance("my-redis-instance",
    redisstorage.WithClientBuilderURL(redisURL))

// Use in session service
sessionService, err := redis.NewService(
    redis.WithRedisInstance("my-redis-instance"),
    redis.WithSessionEventLimit(500),
)
```

## With Summary

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithSessionEventLimit(1000),
    redis.WithSessionTTL(30*time.Minute),

    // Summary configuration
    redis.WithSummarizer(summarizer),
    redis.WithAsyncSummaryNum(4),
    redis.WithSummaryQueueSize(200),
    redis.WithSummaryJobTimeout(120*time.Second),
)
```

## Async Persistence

When async persistence is enabled, `AppendEvent` and `AppendTrackEvent` no longer write to Redis synchronously. Instead, events are dispatched to internal channels and consumed by background worker goroutines. This significantly reduces request latency for write-sensitive scenarios.

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithEnableAsyncPersist(true),
    redis.WithAsyncPersisterNum(10), // 10 worker goroutines
)
```

How it works:

- Each worker goroutine owns one Event channel and one TrackEvent channel (buffer size 100).
- `AppendEvent` selects a channel via `session.Hash % workerNum`, ensuring ordered writes for the same session.
- If the channel is full and the context is cancelled, `context.Canceled` is returned.
- Async write timeout is 2 seconds (`defaultAsyncPersistTimeout`).
- Calling `Close()` closes all channels and waits for workers to drain remaining tasks.

!!! warning "Caution"
    In async persistence mode, events still in the channel may be lost if the process crashes unexpectedly. Evaluate whether to enable this based on your data consistency requirements.

## User Session Index

`WithEnableUserSessionIndex(true)` is an optional capability for HashIdx storage only. It maintains a user-scoped index that maps `userID` to the session IDs created by that user.

The main purpose of this index is to avoid the SCAN operation currently used by `ListSessions`.

This option is intended for fresh HashIdx writes. If you enable it on an environment that already contains historical HashIdx sessions created before the index was introduced, those older sessions will not automatically appear in the index unless you migrate or rebuild the index separately.

## Storage Compatibility Mode (CompatMode)

The new version of Redis Session uses a new storage engine (HashIdx) that distributes data across different Redis Cluster slots by user, eliminating the hotspot issue where all data was concentrated in a single slot in the old version. If you have legacy data to migrate, use `WithCompatMode` to configure compatibility mode for a smooth transition.

!!! tip "In most cases, you don't need to worry about compatibility mode"
    The default `CompatModeLegacy` mode automatically handles read/write compatibility between old and new data — **just upgrade and it works**. You only need to pay attention to compatibility mode configuration in these two cases:

    1. **Heavy use of UserState**: The old and new engines use different Redis keys for UserState. `CreateSession`/`GetSession` only read the new key when merging UserState internally, so legacy UserState data won't automatically carry over to new sessions. If your application relies heavily on UserState, choose the appropriate compatibility mode as described below.
    2. **Large-scale canary deployment**: When old and new instances run simultaneously, use `CompatModeTransition` to ensure mixed-deployment compatibility.

### New Deployments (No Legacy Data)

**Use `CompatModeNone` directly** to skip all compatibility logic and get the best performance:

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithCompatMode(redis.CompatModeNone),
)
```

### Single Node or Small-Scale Upgrade

For single-node deployments, or when you have few enough nodes to upgrade all at once, **just upgrade directly** using the default `CompatModeLegacy` (no explicit configuration needed):

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    // CompatModeLegacy is the default, can be omitted
)
```

In `CompatModeLegacy` mode, newly created sessions use the new storage engine while legacy data remains accessible via fallback reads. Once legacy data expires by TTL, you can switch to `CompatModeNone`.

### Large-Scale Canary Upgrade

For large-scale deployments requiring canary releases where old and new instances run simultaneously, follow these steps:

**Step 1: Canary Phase — Set `CompatModeTransition`**

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithCompatMode(redis.CompatModeTransition),
)
```

In Transition mode, new instances behave identically to old instances (session creation uses legacy storage, UserState dual-writes to both old and new keys), ensuring full data compatibility during mixed deployment.

**Step 2: Full Upgrade Complete — Switch to `CompatModeLegacy`**

After all instances are upgraded, remove the `WithCompatMode` configuration (or explicitly set it to `CompatModeLegacy`). New sessions will use the new storage engine while legacy data remains accessible via fallback reads.

**Step 3: Legacy Data Expired — Switch to `CompatModeNone`**

Once legacy data has expired by TTL (or manually cleaned up if no TTL was set), switch to `CompatModeNone` to remove the compatibility layer.

### Compatibility Mode Reference

| Mode | Session Read | Session Write | UserState Read | UserState Write | Use Case |
| --- | --- | --- | --- | --- | --- |
| `CompatModeNone` | New engine only | New engine only | New key only | New key only | **New deployments**, or all legacy data has expired |
| `CompatModeLegacy` (default) | Legacy first, fallback to new engine | New engine only | New key first, fallback to old key | New key only | **Single/small-scale** direct upgrade |
| `CompatModeTransition` | Legacy first, fallback to new engine | Legacy engine only | New key first, fallback to old key | Dual-write both keys | **Large-scale canary**, mixed old/new instances |

### UserState Migration Notes

The old and new storage engines use different Redis keys for UserState (old: `userstate:{appName}:{userID}`, new: `hashidx:userstate:appName:{userID}`).

- In `CompatModeTransition` mode, `UpdateUserState` writes to both old and new keys. It is recommended to re-write UserState via `UpdateUserState` during the canary phase to sync data to the new key.
- The `ListUserStates` API in Transition and Legacy modes tries the new key first and falls back to the old key if empty. However, `CreateSession`/`GetSession` only read the new key internally when merging UserState, without fallback.
- **AppState is not affected** — `appstate:{appName}` uses the same format in both engines, zero migration cost.

## Use Cases

| Scenario | Recommended Configuration |
| --- | --- |
| New deployment | `CompatModeNone` |
| Single/small-scale upgrade | Default `CompatModeLegacy`, upgrade directly |
| Large-scale canary upgrade | `CompatModeTransition` → `CompatModeLegacy` → `CompatModeNone` |
| Production | Configure TTL, enable async persistence |
| Distributed deployment | Use Redis Cluster |
| High concurrency | Increase AsyncPersisterNum |

## Notes

1. **Connection**: Ensure Redis service is accessible; use connection pooling
2. **TTL management**: Redis natively supports TTL; no additional cleanup tasks needed
3. **Memory management**: Monitor Redis memory usage; configure reasonable maxmemory
4. **High availability**: Use Redis Sentinel or Cluster for production
5. **Priority**: `WithRedisClientURL` has higher priority than `WithRedisInstance`
6. **Async persistence risk**: Events in the channel may be lost on unexpected process exit; evaluate your tolerance
