# Memory Storage

Memory storage is suitable for development environments and small-scale applications. It requires no external dependencies and works out of the box.

## Features

- ✅ No external dependencies
- ✅ Works out of the box
- ✅ High-performance read/write
- ❌ Data not persisted (lost after process restart)
- ❌ No distributed support

## Configuration Options

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | Maximum events per session; oldest events are evicted when exceeded |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | TTL for session state and event list |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | TTL for app-level state |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | TTL for user-level state |
| `WithCleanupInterval(interval time.Duration)` | `time.Duration` | `0` (auto) | Cleanup interval for expired data; defaults to 5 minutes if any TTL is configured |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | Inject session summarizer |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | Number of summary processing workers |
| `WithSummaryQueueSize(size int)` | `int` | `100` | Summary task queue size |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | Timeout for a single summary job |
| `WithAppendEventHook(hooks ...session.AppendEventHook)` | `[]session.AppendEventHook` | `nil` | Add event write hooks |
| `WithGetSessionHook(hooks ...session.GetSessionHook)` | `[]session.GetSessionHook` | `nil` | Add session read hooks |

## Basic Configuration

```go
import "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

// Default configuration (development)
sessionService := inmemory.NewSessionService()
// Result:
// - Up to 1000 events per session
// - All data never expires
// - No automatic cleanup

// Production-like configuration
sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(500),
    inmemory.WithSessionTTL(30*time.Minute),
    inmemory.WithAppStateTTL(24*time.Hour),
    inmemory.WithUserStateTTL(7*24*time.Hour),
    inmemory.WithCleanupInterval(10*time.Minute),
)
// Result:
// - Up to 500 events per session
// - Session expires 30 minutes after last write
// - App state expires after 24 hours
// - User state expires after 7 days
// - Expired data cleaned up every 10 minutes
```

## With Summary

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

summarizer := summary.NewSummarizer(
    summaryModel,
    summary.WithEventThreshold(20),
    summary.WithMaxSummaryWords(200),
)

sessionService := inmemory.NewSessionService(
    inmemory.WithSessionEventLimit(1000),
    inmemory.WithSummarizer(summarizer),
    inmemory.WithAsyncSummaryNum(2),
    inmemory.WithSummaryQueueSize(100),
    inmemory.WithSummaryJobTimeout(60*time.Second),
)
```

## With Hooks

```go
sessionService := inmemory.NewSessionService(
    inmemory.WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
        log.Printf("Appending event to session %s", ctx.Session.ID)
        return next()
    }),
    inmemory.WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
        sess, err := next()
        if err != nil {
            return nil, err
        }
        log.Printf("Got session %s with %d events", sess.ID, len(sess.Events))
        return sess, nil
    }),
)
```

## Use Cases

| Scenario | Recommended Configuration |
| --- | --- |
| Development/Testing | Default configuration |
| Single-node small apps | Configure TTL and EventLimit |
| Demo | Default configuration |
| Unit tests | Default config, create new instance per test |

## Notes

1. **No persistence**: All data is lost after process restart; not suitable for production
2. **Memory usage**: Large numbers of sessions may cause high memory usage; configure reasonable EventLimit and TTL
3. **No distributed support**: Data is not shared across instances; each instance has independent session data
4. **Concurrency safe**: Built-in read-write locks support concurrent access
