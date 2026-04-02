# PGVector Session Storage

> **Example Code**: [examples/session/simple](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/session/simple)

PGVector session storage builds on `session/postgres` and adds pgvector-based semantic recall for session events. It is suitable when you need both durable session persistence and the ability to search historical conversation content by meaning.

All regular PostgreSQL session capabilities still apply here: TTL cleanup, soft delete, summaries, hooks, schema/table prefixing, and async persistence. PGVector adds event embeddings, HNSW indexing, and a search API over stored session events.

## Features

- ✅ Data persistence
- ✅ Distributed support
- ✅ Semantic recall across sessions
- ✅ Hybrid recall (`dense` + PostgreSQL full-text branch)
- ✅ Soft delete support
- ✅ Schema and table prefix support
- ✅ Async persistence support
- ✅ Summary and Hook support

## Configuration Options

### Connection Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithPostgresClientDSN(dsn string)` | `string` | - | PostgreSQL DSN, format: `postgres://user:password@localhost:5432/dbname?sslmode=disable` (highest priority) |
| `WithHost(host string)` | `string` | `localhost` | PostgreSQL server address |
| `WithPort(port int)` | `int` | `5432` | PostgreSQL server port |
| `WithUser(user string)` | `string` | `""` | Database username |
| `WithPassword(password string)` | `string` | `""` | Database password |
| `WithDatabase(database string)` | `string` | `trpc-agent-go-pgsession` | Database name |
| `WithSSLMode(sslMode string)` | `string` | `disable` | SSL mode: `disable`, `require`, `verify-ca`, `verify-full` |
| `WithPostgresInstance(name string)` | `string` | - | Use a pre-configured PostgreSQL instance (lowest priority) |
| `WithExtraOptions(extraOptions ...any)` | `[]any` | `nil` | Extra options for the PostgreSQL client |

**Priority**: `WithPostgresClientDSN` > `WithHost/Port/User/Password/Database` > `WithPostgresInstance`

### Session Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithSessionEventLimit(limit int)` | `int` | `1000` | Maximum events per session |
| `WithSessionTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | Session TTL |
| `WithAppStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | App state TTL |
| `WithUserStateTTL(ttl time.Duration)` | `time.Duration` | `0` (no expiry) | User state TTL |
| `WithCleanupInterval(interval time.Duration)` | `time.Duration` | `0` (auto) | TTL cleanup interval; defaults to 5 minutes if any TTL is configured |
| `WithSoftDelete(enable bool)` | `bool` | `true` | Enable or disable soft delete |

### Async Persistence Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithEnableAsyncPersist(enable bool)` | `bool` | `false` | Enable async persistence for session and track events |
| `WithAsyncPersisterNum(num int)` | `int` | `10` | Number of async persistence workers |

### Summary Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithSummarizer(s summary.SessionSummarizer)` | `summary.SessionSummarizer` | `nil` | Inject session summarizer |
| `WithAsyncSummaryNum(num int)` | `int` | `3` | Number of summary processing workers |
| `WithSummaryQueueSize(size int)` | `int` | `100` | Summary task queue size |
| `WithSummaryJobTimeout(timeout time.Duration)` | `time.Duration` | `60s` | Timeout for a single summary job |

### Schema and Table Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithSchema(schema string)` | `string` | `""` (default schema) | Specify schema name |
| `WithTablePrefix(prefix string)` | `string` | `""` | Table name prefix |
| `WithSkipDBInit(skip bool)` | `bool` | `false` | Skip automatic database initialization |

### Hook Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithAppendEventHook(hooks ...session.AppendEventHook)` | `[]session.AppendEventHook` | `nil` | Add event write hooks |
| `WithGetSessionHook(hooks ...session.GetSessionHook)` | `[]session.GetSessionHook` | `nil` | Add session read hooks |

### Vector and Search Configuration

| Option | Type | Default | Description |
| --- | --- | --- | --- |
| `WithEmbedder(e embedder.Embedder)` | `embedder.Embedder` | required | Embedder used for event embeddings and query embeddings |
| `WithIndexDimension(dim int)` | `int` | `1536` | Embedding dimension; should match the configured embedder |
| `WithEmbedTimeout(timeout time.Duration)` | `time.Duration` | `30s` | Timeout for embedding API calls |
| `WithSyncIndexing(sync bool)` | `bool` | `false` | Generate embeddings synchronously after event persistence |
| `WithIndexTextBuilder(builder IndexTextBuilder)` | `IndexTextBuilder` | `nil` | Customize the text written to `content_text` before embedding |
| `WithMaxResults(n int)` | `int` | `5` | Default result count for `SearchEvents` |
| `WithHNSWM(m int)` | `int` | `16` | HNSW index `m` parameter |
| `WithHNSWEfConstruction(ef int)` | `int` | `200` | HNSW index `ef_construction` parameter |
| `WithHybridRRFK(k int)` | `int` | `60` | Reciprocal Rank Fusion constant for hybrid search |
| `WithHybridCandidateRatio(ratio int)` | `int` | `3` | Candidate multiplier per hybrid branch before fusion |

## Basic Configuration

```go
import (
    "time"

    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    sessionpgvector "trpc.group/trpc-go/trpc-agent-go/session/pgvector"
)

embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
)

sessionService, err := sessionpgvector.NewService(
    sessionpgvector.WithPostgresClientDSN(
        "postgres://user:password@localhost:5432/trpc_sessions?sslmode=disable",
    ),
    sessionpgvector.WithEmbedder(embedder),
    sessionpgvector.WithIndexDimension(embedder.GetDimensions()),
    sessionpgvector.WithSessionTTL(30*time.Minute),
    sessionpgvector.WithSoftDelete(true),
)
if err != nil {
    return err
}
defer sessionService.Close()
```

## Instance Reuse

```go
import (
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    sessionpg "trpc.group/trpc-go/trpc-agent-go/session/pgvector"
    "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

postgres.RegisterPostgresInstance("my-postgres-instance",
    postgres.WithClientConnString(
        "postgres://user:password@localhost:5432/trpc_sessions?sslmode=disable",
    ),
)

embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
)

sessionService, err := sessionpg.NewService(
    sessionpg.WithPostgresInstance("my-postgres-instance"),
    sessionpg.WithEmbedder(embedder),
    sessionpg.WithIndexDimension(embedder.GetDimensions()),
)
```

## Semantic Recall

`*pgvector.Service` also implements `session.SearchableService`, so you can search a user's historical messages directly after creating the service:

```go
import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/session"
)

ctx := context.Background()

results, err := sessionService.SearchEvents(ctx, session.EventSearchRequest{
    Query: "travel plan",
    UserKey: session.UserKey{
        AppName: "my-agent",
        UserID:  "user123",
    },
    SearchMode: session.SearchModeHybrid,
    MaxResults: 5,
})
if err != nil {
    return err
}

for _, hit := range results {
    fmt.Printf("[%0.3f] %s %s\n", hit.Score, hit.SessionKey.SessionID, hit.Text)
}
```

### Search Request Fields

| Field | Description |
| --- | --- |
| `Query` | Required query text for semantic recall |
| `UserKey` | Required search scope: `<appName, userID>` |
| `SessionIDs` | Restrict recall to specific session IDs |
| `ExcludeSessionIDs` | Exclude specific session IDs from recall |
| `Roles` | Limit matches to specific message roles |
| `CreatedAfter` / `CreatedBefore` | Restrict results by event time window |
| `FilterKey` | Filter events by hierarchical branch/filter key |
| `MaxResults` | Override backend default result count |
| `MinScore` | Dense similarity threshold; most useful with `SearchModeDense` |
| `SearchMode` | `session.SearchModeDense` (default) or `session.SearchModeHybrid` |
| `HybridRRFK` | Override the default RRF constant for hybrid recall |
| `HybridCandidateRatio` | Override how many candidates each hybrid branch fetches before fusion |

**Search modes**

- `SearchModeDense`: embedding similarity only
- `SearchModeHybrid`: dense retrieval + PostgreSQL full-text retrieval, fused with Reciprocal Rank Fusion (RRF)

### LLMAgent Recall Preload

Because `*pgvector.Service` implements `session.SearchableService`, LLMAgent can
automatically use the current user message as a recall query and inject matched
events from other sessions into the system prompt:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session"
)

assistant := llmagent.New(
    "assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithPreloadSessionRecall(5),
    llmagent.WithPreloadSessionRecallMinScore(0.70),
    llmagent.WithPreloadSessionRecallSearchMode(session.SearchModeHybrid),
)

r := runner.NewRunner("my-app", assistant,
    runner.WithSessionService(sessionService),
)
```

Notes:

- Recall is scoped to the same user and automatically excludes the current session, so only **other sessions** are searched.
- The recalled snippets are **merged into the system message** and labeled as untrusted historical data.
- If the current request has no text query, the backend returns no hits, or a sub-flow uses `include_contents="none"`, recall preload is skipped.
- Call `SearchEvents` directly when you need explicit filters such as `SessionIDs`, `Roles`, or time windows.

## Indexing Behavior

PGVector indexes events after they are persisted:

- Only persisted user/assistant text events are indexed
- Tool calls, tool results, partial events, and empty content are skipped
- By default, embeddings are generated asynchronously after the write succeeds
- If you need a newly appended event to be searchable immediately, use `WithSyncIndexing(true)`
- Embedding failures do not roll back the session write; they are logged as warnings
- `WithIndexTextBuilder(...)` lets you enrich or normalize the text before it is embedded and stored in `content_text`

This means semantic recall is **eventually consistent** by default.

## Database Initialization

Unless `WithSkipDBInit(true)` is enabled, the service initializes PostgreSQL automatically:

1. Enables the `pgvector` extension with `CREATE EXTENSION IF NOT EXISTS vector`
2. Creates the same core tables used by `session/postgres`
3. Extends `session_events` with vector-search columns
4. Creates a GIN index for the keyword branch and an HNSW index for dense recall

If HNSW index creation fails, the service logs a warning and continues running. Dense recall still works, but it may fall back to slower query plans depending on your PostgreSQL/pgvector setup.

If you use `WithSkipDBInit(true)`, make sure the extension, tables, columns, and indexes already exist, and that the `embedding` column dimension matches `WithIndexDimension(...)`.

## Storage Structure

All regular session tables are the same as `session/postgres`. PGVector extends `session_events` with additional search fields:

| Column | Type | Purpose |
| --- | --- | --- |
| `content_text` | `TEXT` | Indexed text returned in recall results |
| `role` | `VARCHAR(32)` | Normalized role used for filtering and display |
| `embedding` | `vector(N)` | Event embedding used for dense recall |
| `search_vector` | `tsvector` | Generated PostgreSQL text-search vector for the hybrid keyword branch |

Additional indexes:

- `GIN(search_vector)` for the PostgreSQL full-text branch
- `HNSW(embedding vector_cosine_ops)` for vector similarity recall

## Use Cases

| Scenario | Recommended Configuration |
| --- | --- |
| Durable chat history with semantic lookup | Default setup + `WithEmbedder(...)` |
| Cross-session recall for the same user | Use `SearchEvents` with `SearchModeHybrid` |
| Low-latency writes | Keep default async indexing |
| Immediate recall after append | Enable `WithSyncIndexing(true)` |
| Multi-tenant PostgreSQL | Configure `WithSchema(...)` and/or `WithTablePrefix(...)` |

## Notes

1. **Embedder is required**: `NewService()` returns an error if `WithEmbedder(...)` is missing.
2. **Dimensions must match**: if the embedder reports a dimension, it must match `WithIndexDimension(...)`.
3. **Text search language**: the built-in PostgreSQL text-search branch uses `to_tsvector('english', content_text)`. Dense recall still works for non-English content, but hybrid keyword behavior follows PostgreSQL's `english` text-search configuration.
4. **Permissions**: automatic initialization requires permission to create extensions, tables, and indexes.
5. **Resource cleanup**: call `Close()` when the service is no longer needed.
