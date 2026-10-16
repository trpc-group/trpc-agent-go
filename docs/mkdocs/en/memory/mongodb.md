# MongoDB Storage

For common Agent integration, extraction modes, and tool configuration, see
[Usage and Configuration](usage.md).

## Overview

The MongoDB backend (`memory/mongodb`) implements `memory.Service` on top of
MongoDB. It is the first MongoDB-based memory backend in the framework and
reuses the `storage/mongodb` connection layer, so it shares the same client
builder and instance registry as other MongoDB-based components.

**Use case**: Production, document-oriented storage, flexible schema

**Features**:

- Persistent document storage, one document per memory entry
- Optional soft delete (tombstones) with automatic revival on re-add
- Idempotent adds and canonical-ID rotation on update, consistent with the
  other relational backends
- Scope isolation on `<appName, userID>` with dedicated indexes
- BM25-style keyword search computed in the application layer, consistent
  with the `mysql` / `postgres` backends
- Works on standalone MongoDB deployments (no multi-document transaction
  requirement)

## Usage

Connect directly with a MongoDB URI (recommended):

```go
import memorymongodb "trpc.group/trpc-go/trpc-agent-go/memory/mongodb"

mongodbService, err := memorymongodb.NewService(
    memorymongodb.WithMongoClientURI("mongodb://localhost:27017"),
    memorymongodb.WithSoftDelete(true),
)
if err != nil {
    panic(err)
}
defer mongodbService.Close()
```

Or register a named instance through the `storage/mongodb` registry and
reference it by name:

```go
import (
    memorymongodb "trpc.group/trpc-go/trpc-agent-go/memory/mongodb"
    storagemongodb "trpc.group/trpc-go/trpc-agent-go/storage/mongodb"
)

storagemongodb.RegisterMongoDBInstance("memory-mongo",
    storagemongodb.WithClientBuilderDSN("mongodb://localhost:27017"),
)

mongodbService, err := memorymongodb.NewService(
    memorymongodb.WithMongoInstance("memory-mongo"),
)
if err != nil {
    panic(err)
}
defer mongodbService.Close()
```

`WithMongoClientURI` takes priority over `WithMongoInstance` when both are
set. One of them is required; otherwise `NewService` returns an error.

## Configuration Options

| Option | Description | Default |
| ------ | ----------- | ------- |
| `WithMongoClientURI(uri)` | MongoDB connection URI, e.g. `mongodb://user:pass@host1:27017,host2:27017/?replicaSet=rs0`. Highest priority | (required, one of URI/instance) |
| `WithMongoInstance(name)` | Use an instance registered via `storage/mongodb.RegisterMongoDBInstance` | (required, one of URI/instance) |
| `WithDatabase(name)` | Database name | `"trpc-agent-go-mongo-memory"` |
| `WithCollectionName(name)` | Collection name. Must start with a letter or underscore, contain only alphanumerics and underscores, max 64 characters; panics if invalid | `"memories"` |
| `WithSkipDBInit(skip)` | Skip index creation. Useful without `createIndex` permission, when indexes are managed by migration tools, or pre-created in production | `false` |
| `WithSoftDelete(enabled)` | Enable soft delete: deletes set `deleted_at` and queries filter tombstones | `false` (hard delete) |
| `WithMemoryLimit(limit)` | Maximum memories per user. `0` disables the limit check | `1000` |
| `WithMinSearchScore(score)` | Minimum keyword-search score; lower-scored results are filtered. Negative values are ignored | `0.3` |
| `WithMaxResults(n)` | Maximum keyword-search results. `0` disables truncation; negative values are ignored | `10` |
| `WithCustomTool(toolName, creator)` | Register a custom memory tool; enabled by default | — |
| `WithToolEnabled(toolName, enabled)` | Enable/disable a memory tool; takes precedence over auto mode defaults regardless of option order | — |
| `WithAutoMemoryExposedTools(names...)` | Expose enabled tools via `Tools()` in auto memory mode | — |
| `WithToolExposed(toolName, exposed)` | Control whether an enabled tool is exposed via `Tools()` | — |
| `WithExtractor(e)` | Set the memory extractor to enable auto memory mode | — |
| `WithAsyncMemoryNum(n)` | Number of async memory workers. Values `< 1` reset to the default | `1` |
| `WithMemoryQueueSize(size)` | Queue size for memory jobs. Values `< 1` reset to the default | `10` |
| `WithMemoryJobTimeout(d)` | Timeout for each memory extraction job | `30s` |
| `WithDisableAutoMemoryOnExternalContext(disable)` | Skip auto extraction for sessions that consumed framework-owned external context | `false` |
| `WithExtraOptions(...)` | Extra options passed to the MongoDB client builder | — |

## Storage Schema and Indexes

Each memory entry is stored as one document:

```text
{
    app_name:    string,           // application name
    user_id:     string,           // user ID
    memory_id:   string,           // canonical memory ID (SHA256 hex)
    memory_data: string,           // json.Marshal(memory.Entry), JSON string
    created_at:  time,
    updated_at:  time,
    deleted_at:  time | absent     // only present on soft-deleted tombstones
}
```

`memory_data` is a JSON string of `memory.Entry`, matching the wire contract
of the `memory.Memory` JSON tags exactly (same as the `memory_data` column of
the MySQL backend). Scalar fields exist for indexing and filtering.

Two indexes are created automatically at service construction (with a 30
second timeout) unless `WithSkipDBInit(true)` is set:

- **Unique index** `(app_name, user_id, memory_id)` with a partial filter
  `deleted_at: null`, so soft-deleted tombstones do not occupy the unique
  slot and can be revived in place.
- **Query index** `(app_name, user_id)` for per-user reads, searches, and
  clears.

## Soft Delete

Soft delete is disabled by default (hard delete). With
`WithSoftDelete(true)`, `DeleteMemory` and `ClearMemories` set `deleted_at`
instead of removing documents, and all queries filter on `deleted_at: null`.
An idempotent `AddMemory` of the same content revives the matching tombstone
via `$unset` on `deleted_at`, preserving its original `created_at`; the same
revival applies when an update rotates onto a tombstoned canonical ID.

## Search Behavior

`SearchMemories` loads the user's active memories and ranks them with a
normalized BM25-style keyword score computed in the application layer —
the same behavior as the `mysql` and `postgres` backends. Results below
`WithMinSearchScore` are filtered and truncated to `WithMaxResults`. An empty
query returns an empty result.

**Vector search is not supported.** If you need semantic similarity search,
use the pgvector, mysqlvec, sqlitevec, or ChromaDB backends, or plan on a
future integration with MongoDB Atlas `vectorSearch`.

## Integration Tests

Unit tests run without external dependencies. Integration tests against a
real MongoDB deployment are gated by the `integration` build tag and the
`MONGODB_INTEGRATION_URI` environment variable; they are skipped when the
variable is unset:

```bash
export MONGODB_INTEGRATION_URI="mongodb://localhost:27017"
cd memory/mongodb && go test -tags=integration ./...
```

Each integration test uses a unique database that is dropped when the test
finishes. The backend does not require multi-document transactions, so a
standalone (non-replica-set) deployment is sufficient.

**Resource cleanup**: call `Close()` to stop async workers and disconnect the
MongoDB client. `Close` is idempotent.
