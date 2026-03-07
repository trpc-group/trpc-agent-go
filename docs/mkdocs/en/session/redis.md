# Redis Storage

Redis storage is suitable for production environments and distributed applications, providing high performance and automatic expiration capabilities.

## Features

- ✅ Data persistence
- ✅ Distributed support
- ✅ High-performance read/write
- ✅ Native TTL support
- ✅ Async persistence support

## Configuration Options

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithRedisClientURL(url string)` | `string` | - | Create Redis client via URL, format: `redis://[username:password@]host:port[/database]` |
| `WithRedisInstance(instanceName string)` | `string` | - | Use a pre-configured Redis instance (lower priority than URL) |
| `WithExtraOptions(extraOptions ...any)` | `[]any` | `nil` | Extra options for the Redis client |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | Maximum events per session |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | TTL for session state and events |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | TTL for app-level state |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | TTL for user-level state |
| `WithEnableAsyncPersist(enable bool)` | `bool` | `false` | Enable async persistence |
| `WithAsyncPersisterNum(num int)` | `int` | `10` | Number of async persistence workers |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | Inject session summarizer |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | Number of summary processing workers |
| `WithSummaryQueueSize(size int)` | `int` | `100` | Summary task queue size |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | Timeout for a single summary job |
| `WithKeyPrefix(prefix string)` | `string` | `""` | Redis key prefix; all keys will start with `prefix:` |
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
// Result:
// - Connect to local Redis database 0
// - Up to 1000 events per session
// - Session expires 30 minutes after last write (Redis TTL)
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

redisURL := "redis://127.0.0.1:6379"
redisstorage.RegisterRedisInstance("my-redis-instance",
    redisstorage.WithClientBuilderURL(redisURL))

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

    redis.WithSummarizer(summarizer),
    redis.WithAsyncSummaryNum(4),
    redis.WithSummaryQueueSize(200),
)
```

## Async Persistence

Enable async persistence to improve write performance:

```go
sessionService, err := redis.NewService(
    redis.WithRedisClientURL("redis://localhost:6379"),
    redis.WithEnableAsyncPersist(true),
    redis.WithAsyncPersisterNum(10),
)
```

## Storage Structure

Redis storage uses the following key structure:

```
# App state
appstate:{appName} -> Hash {key: value}

# User state
userstate:{appName}:{userID} -> Hash {key: value}

# Session data
sess:{appName}:{userID} -> Hash {sessionID: SessionData(JSON)}

# Events
event:{appName}:{userID}:{sessionID} -> SortedSet {score: timestamp, value: Event(JSON)}

# Track events
track:{appName}:{userID}:{sessionID}:{trackName} -> SortedSet {score: timestamp, value: TrackEvent(JSON)}

# Summary data (optional)
sesssum:{appName}:{userID} -> Hash {sessionID:filterKey: Summary(JSON)}
```

## Use Cases

| Scenario | Recommended Configuration |
| --- | --- |
| Production | Configure TTL, enable async persistence |
| Distributed deployment | Use Redis Cluster |
| High concurrency | Increase AsyncPersisterNum |
| Data persistence needed | Configure Redis persistence strategy |

## Notes

1. **Connection**: Ensure Redis service is accessible; use connection pooling
2. **TTL management**: Redis natively supports TTL; no additional cleanup tasks needed
3. **Memory management**: Monitor Redis memory usage; configure reasonable maxmemory
4. **High availability**: Use Redis Sentinel or Cluster for production
5. **Priority**: `WithRedisClientURL` has higher priority than `WithRedisInstance`
