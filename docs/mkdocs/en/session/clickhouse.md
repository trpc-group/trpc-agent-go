# ClickHouse Storage

ClickHouse storage is suitable for production environments and massive data scenarios, leveraging ClickHouse's powerful write throughput and data compression capabilities.

## Features

- ✅ Data persistence
- ✅ Distributed support
- ✅ Massive data storage
- ✅ High write throughput
- ✅ Data compression
- ✅ Batch write support
- ✅ Table prefix support

## Configuration Options

### Connection Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithClickHouseDSN(dsn string)` | `string` | - | ClickHouse DSN (recommended), format: `clickhouse://user:password@host:port/database?dial_timeout=10s` |
| `WithClickHouseInstance(name string)` | `string` | - | Use a pre-configured ClickHouse instance (lower priority than DSN) |
| `WithExtraOptions(opts ...any)` | `[]any` | `nil` | Extra options for the ClickHouse client |

**Priority**: `WithClickHouseDSN` > `WithClickHouseInstance`

### Session Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | Maximum events per session |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | Session TTL |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | App state TTL |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | User state TTL |
| `WithDeletedRetention(retention time.Duration)` | `time.Duration` | `0` (disabled) | Soft-deleted data retention period. When enabled, periodically cleans up via `ALTER TABLE DELETE`. **Not recommended for production** — prefer ClickHouse table-level TTL |
| `WithCleanupInterval(interval time.Duration)` | `time.Duration` | `0` (auto) | Cleanup task interval |

### Async Persistence Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithEnableAsyncPersist(enable bool)` | `bool` | `false` | Enable application-level async persistence |
| `WithAsyncPersisterNum(num int)` | `int` | `10` | Number of async workers |
| `WithBatchSize(size int)` | `int` | `100` | Batch write size |
| `WithBatchTimeout(timeout time.Duration)` | `time.Duration` | `100ms` | Batch write timeout |

### Summary Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | Inject session summarizer |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | Number of summary processing workers |
| `WithSummaryQueueSize(size int)` | `int` | `100` | Summary task queue size |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | Timeout for a single summary job |

### Schema Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithTablePrefix(prefix string)` | `string` | `""` | Table name prefix |
| `WithSkipDBInit(skip bool)` | `bool` | `false` | Skip automatic table creation |

### Hook Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithAppendEventHook(hooks ...session.AppendEventHook)` | `[]session.AppendEventHook` | `nil` | Add event write hooks |
| `WithGetSessionHook(hooks ...session.GetSessionHook)` | `[]session.GetSessionHook` | `nil` | Add session read hooks |

## Basic Configuration

```go
import "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"

sessionService, err := clickhouse.NewService(
    clickhouse.WithClickHouseDSN("clickhouse://default:password@localhost:9000/default"),
)
```

## Instance Reuse

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
    sessionch "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
)

clickhouse.RegisterClickHouseInstance("my-clickhouse",
    clickhouse.WithClientBuilderDSN("clickhouse://localhost:9000/default"),
)

sessionService, err := sessionch.NewService(
    sessionch.WithClickHouseInstance("my-clickhouse"),
)
```

## Batch Write Configuration

ClickHouse is optimized for batch writes. Configure batch parameters for better performance:

```go
sessionService, err := clickhouse.NewService(
    clickhouse.WithClickHouseDSN("clickhouse://localhost:9000/default"),
    clickhouse.WithEnableAsyncPersist(true),
    clickhouse.WithAsyncPersisterNum(10),
    clickhouse.WithBatchSize(100),
    clickhouse.WithBatchTimeout(100*time.Millisecond),
)
```

## With Summary

```go
sessionService, err := clickhouse.NewService(
    clickhouse.WithClickHouseDSN("clickhouse://default:password@localhost:9000/default"),
    clickhouse.WithSummarizer(summarizer),
    clickhouse.WithAsyncSummaryNum(2),
)
```

## Storage Structure

The ClickHouse implementation uses the `ReplacingMergeTree` engine for data updates and deduplication.

**Key characteristics:**

1. **ReplacingMergeTree**: Using the `updated_at` field, ClickHouse automatically merges records with the same primary key in the background, keeping the latest version
2. **FINAL queries**: All read operations use the `FINAL` keyword (e.g., `SELECT ... FINAL`) to ensure data parts are merged at query time for read consistency
3. **Soft Delete**: Delete operations are implemented by inserting a new record with a `deleted_at` timestamp; queries filter by `deleted_at IS NULL`

### session_states

```sql
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
ORDER BY (app_name, user_id, session_id)
SETTINGS allow_nullable_key = 1
COMMENT 'Session states table';
```

### session_events

```sql
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
ORDER BY (app_name, user_id, session_id, event_id)
SETTINGS allow_nullable_key = 1
COMMENT 'Session events table';
```

### session_summaries

```sql
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
ORDER BY (app_name, user_id, session_id, filter_key)
SETTINGS allow_nullable_key = 1
COMMENT 'Session summaries table';
```

### app_states

```sql
CREATE TABLE IF NOT EXISTS app_states (
    app_name    String,
    key         String COMMENT 'State key',
    value       String COMMENT 'State value',
    updated_at  DateTime64(6),
    expires_at  Nullable(DateTime64(6)) COMMENT 'Expiration time (application-level)',
    deleted_at  Nullable(DateTime64(6)) COMMENT 'Soft delete timestamp'
) ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY app_name
ORDER BY (app_name, key)
SETTINGS allow_nullable_key = 1
COMMENT 'Application states table';
```

### user_states

```sql
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
ORDER BY (app_name, user_id, key)
SETTINGS allow_nullable_key = 1
COMMENT 'User states table';
```

## Use Cases

| Scenario | Recommended Configuration |
| --- | --- |
| Massive log storage | Enable batch writes, configure reasonable BatchSize |
| High-concurrency writes | Enable async persistence, increase worker count |
| Data analytics | Use ClickHouse native query capabilities |
| Long-term data retention | Use ClickHouse table-level TTL |

## Notes

1. **ClickHouse version**: Requires ClickHouse 22.3+ for JSON type support
2. **ReplacingMergeTree**: Data updates are implemented by inserting new records; background auto-merge handles deduplication
3. **FINAL queries**: Using FINAL at read time ensures consistency but may impact performance
4. **Soft delete cleanup**: `WithDeletedRetention` uses `ALTER TABLE DELETE`, which may have performance impact on large datasets; prefer ClickHouse Native TTL
5. **Batch writes**: ClickHouse is optimized for batch writes; avoid frequent small batch writes
6. **Partition strategy**: Default partitioning by `app_name` and `user_id` hash is suitable for most scenarios
