# MongoDB Storage

MongoDB storage is suitable for production environments that prefer document
storage while still requiring persistent session state, event history, track
events, and summaries.

## Requirements

MongoDB session storage requires a deployment that supports multi-document
transactions, such as a replica set or sharded cluster. Standalone MongoDB
deployments are not supported because event and track persistence update session
state and append history in one transaction.

## Features

- ✅ Data persistence
- ✅ Distributed support
- ✅ Soft delete support
- ✅ Collection prefix support
- ✅ Async persistence support
- ✅ Track service support
- ✅ Event window support

## Configuration Options

### Connection Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithMongoClientURI(uri string)` | `string` | - | MongoDB URI, for example `mongodb://user:pass@host1:27017,host2:27017/?replicaSet=rs0` |
| `WithMongoInstance(instanceName string)` | `string` | - | Use a pre-configured MongoDB instance (lower priority than URI) |
| `WithDatabase(database string)` | `string` | `trpc-agent-go-mongo-session` | MongoDB database name |
| `WithExtraOptions(extraOptions ...any)` | `[]any` | `nil` | Extra options for customized client builders |

**Priority**: `WithMongoClientURI` > `WithMongoInstance`

### Session Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | Maximum events per session in context-window mode |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | Session TTL |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | App state TTL |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | User state TTL |
| `WithCleanupInterval(interval time.Duration)` | `time.Duration` | `0` (auto) | Event and track cleanup interval; defaults to 5 minutes if session TTL is configured |
| `WithSoftDelete(enable bool)` | `bool` | `true` | Enable or disable soft delete |

### Async Persistence Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithEnableAsyncPersist(enable bool)` | `bool` | `false` | Enable async persistence for session events and track events |
| `WithAsyncPersisterNum(num int)` | `int` | `10` | Number of async persistence workers |

### Summary Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | Inject session summarizer |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | Number of summary processing workers |
| `WithSummaryQueueSize(size int)` | `int` | `100` | Summary task queue size |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | Timeout for a single summary job |
| `WithSummaryFilterAllowlist(filterKeys ...string)` | `[]string` | `nil` | Restrict branch summary filter keys |
| `WithCascadeFullSessionSummary(enable bool)` | `bool` | `true` | Refresh full-session summary after allowed branch summaries |

### Collection Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithCollectionPrefix(prefix string)` | `string` | `""` | Collection name prefix |
| `WithSkipDBInit(skip bool)` | `bool` | `false` | Skip automatic index initialization and transaction probe |

### Hook Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithAppendEventHook(hooks ...session.AppendEventHook)` | `[]session.AppendEventHook` | `nil` | Add event write hooks |
| `WithGetSessionHook(hooks ...session.GetSessionHook)` | `[]session.GetSessionHook` | `nil` | Add session read hooks |

## Basic Configuration

```go
import "trpc.group/trpc-go/trpc-agent-go/session/mongodb"

sessionService, err := mongodb.NewService(
    mongodb.WithMongoClientURI("mongodb://user:password@localhost:27017/?replicaSet=rs0"),
    mongodb.WithDatabase("trpc_agent_go"),
)
```

## Instance Reuse

```go
import (
    storagemongodb "trpc.group/trpc-go/trpc-agent-go/storage/mongodb"
    sessionmongodb "trpc.group/trpc-go/trpc-agent-go/session/mongodb"
)

storagemongodb.RegisterMongoDBInstance("my-mongodb-instance",
    storagemongodb.WithClientBuilderDSN("mongodb://user:password@localhost:27017/?replicaSet=rs0"),
)

sessionService, err := sessionmongodb.NewService(
    sessionmongodb.WithMongoInstance("my-mongodb-instance"),
    sessionmongodb.WithDatabase("trpc_agent_go"),
)
```

## Collection Prefix

MongoDB supports collection prefix configuration for multi-application shared
database scenarios:

```go
sessionService, err := mongodb.NewService(
    mongodb.WithMongoClientURI("mongodb://user:password@localhost:27017/?replicaSet=rs0"),
    mongodb.WithCollectionPrefix("app1_"), // app1_session_states
)
```

## Expiration and Cleanup

Session state, app state, and user state use MongoDB TTL indexes on `expires_at`.
Summaries do not have an independent TTL and follow the session lifecycle.

Session events and track events intentionally do not use TTL indexes. They are
cleaned by the service as whole session groups so a session's history does not
partially disappear while the session is still active. Dedicated cleanup indexes
on `updated_at` support these grouped cleanup scans.

## Storage Structure

MongoDB uses these collections:

- `session_states`
- `session_events`
- `session_tracks`
- `session_summaries`
- `app_states`
- `user_states`

## Integration Tests

Integration tests require a MongoDB replica set or sharded cluster and are gated
by the `integration` build tag and `MONGODB_INTEGRATION_URI`:

```bash
MONGODB_INTEGRATION_URI='mongodb://user:pass@host:27017/?replicaSet=rs0' \
  go test -tags=integration -count=1 ./...
```
