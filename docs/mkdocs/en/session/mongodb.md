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
| `WithTrackEventTTL(ttl time.Duration)` | `time.Duration` | Inherits SessionTTL | Track event TTL. Non-positive values disable track event expiry |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | App state TTL |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | User state TTL |
| `WithCleanupInterval(interval time.Duration)` | `time.Duration` | `0` (auto) | Event and track cleanup interval; defaults to 5 minutes if session or track event TTL is configured |
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

Session events and track events intentionally do not use TTL indexes. By default,
they are cleaned by the service as whole session groups so a session's history
does not partially disappear while the session is still active. If
`WithTrackEventTTL` is configured, track events use that TTL independently.
Dedicated cleanup indexes on `updated_at` support these cleanup scans.

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

## Expiration cleanup query costs

Cleanup first selects session groups containing non-deleted rows whose
`updated_at` is at or before the TTL cutoff. It then checks each candidate for
a non-deleted row newer than the cutoff, using the existing session-leading
cleanup index with at most eight concurrent probes. A group with any newer
row is retained, including its older rows. Track groups still span all tracks in the same session.

Initialization adds a non-unique partial `cleanup_candidates` index to both
event collections. Existing documents, cleanup indexes, TTL settings and
delete modes remain unchanged. For externally managed indexes, create these
before rollout, adjusting collection/index names for any prefix:

```javascript
for (const name of ["session_events", "session_tracks"]) {
  db.getCollection(name).createIndex(
    {updated_at: 1, app_name: 1, user_id: 1, session_id: 1},
    {name: "idx_" + name + "_cleanup_candidates",
     partialFilterExpression: {deleted_at: null}}
  )
}
```

This avoids scanning recent histories to find candidates, at the cost of an
additional index and one short query per candidate session. When most history
is already old, candidate enumeration can still be large; measure query counts
and latency as well as scanned documents. Missing indexes affect performance,
not correctness, and older service versions can continue using the same data.
Prebuild indexes on large collections to avoid startup-time index build load.

Candidates are processed in batches of at most 500. Each completed batch
commits its expired groups, even if fewer than 500 have expired; a failure in a
later batch does not discard earlier deletions. Event and track cleanup run
sequentially with separate 30-second budgets (up to 60 seconds combined), so
an event cleanup timeout does not consume the track cleanup budget. A shorter
parent deadline or service shutdown still cancels both, and cleanup waits for
all probe workers to exit.

Cleanup keeps the existing scan-then-delete concurrency model: the candidate
probe and subsequent deletion are not atomic with concurrent event appends.
It does not add coordination between service instances or change the cleanup
interval.

## Event ordering index upgrades

New services create `idx_session_events_ordered_lookup`, a non-unique partial
index on `(app_name, user_id, session_id, created_at, _id)` for documents matching
`{deleted_at: null}`. Collection prefixes also apply to the index name. It
supports chronological event reads and reverse pagination without an additional
sort. The original lookup index is retained for rolling upgrades and rollback;
existing documents and older writers need no conversion.

Index creation during initialization can add startup time and database load.
For large collections, build the index before deploying the new service. If
using `WithSkipDBInit(true)`, manage the index explicitly (adjust names for your
collection prefix):

```javascript
db.session_events.createIndex(
  {app_name: 1, user_id: 1, session_id: 1, created_at: 1, _id: 1},
  {name: "idx_session_events_ordered_lookup",
   partialFilterExpression: {deleted_at: null}}
)
```

Reads remain correct without this index, but may need a blocking sort. Keeping
both indexes adds storage and write overhead. This index alone does not limit
the number of events loaded by a context-window read.

## Context-window read costs

For a single session with a positive event limit, MongoDB reads newest events
first. The initial cursor batch contains at most the smaller of the event limit
and 128 documents. Subsequent batches contain up to 128 documents, including
when skipping empty payloads or searching older history for a user message.
The reader closes the cursor once the window and its required user message are
available. The returned order and filtering remain the same. If the window has
no user message, the reader searches older history for the nearest user message;
a history without any user message still requires a full scan. Empty payloads
do not consume an event slot.

Time filtering uses the event payload timestamp, which can differ from the
document's `created_at`. If the oldest event in the candidate window satisfies
the time cutoff, the window is safe to use. Otherwise, the reader falls back to
the original full-history filtering to preserve the first-matching-timestamp
behavior even when timestamps are out of order. Unlimited and multi-session
reads retain the existing full-history path. Pagination is unchanged. The
ordered lookup index avoids a blocking sort; the optimization does not force
an index hint, so externally managed indexes remain supported.

Bounded reads validate only payloads actually traversed, including older events
searched for a user message. Unlike the previous full-history context reads,
they can succeed despite invalid JSON in unvisited historical payloads. Invalid
JSON encountered during traversal still returns an error; full-history
fallbacks still decode the complete history. A bounded read does not validate
the integrity of the entire session history.

An optional live-model smoke test verifies a three-turn conversation, event
window truncation and history reloading after recreating the service. It sends
synthetic prompts to the configured endpoint and is skipped by default:

```bash
# Configure OPENAI_API_KEY, OPENAI_BASE_URL and MODEL_NAME in your environment.
cd test
MONGODB_LIVE_MODEL_TEST=1 MONGO_TEST_URI='mongodb://localhost:27017/?replicaSet=rs0' \
  go test -run '^TestMongoDBHistoryLiveModel$' -count=1 -v .
```
