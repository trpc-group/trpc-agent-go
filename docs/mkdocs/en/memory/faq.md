# FAQ

## Difference between Memory and Session

Memory and Session solve different problems:

| Dimension     | Memory                           | Session                               |
| ------------- | -------------------------------- | ------------------------------------- |
| **Purpose**   | Long-term user profile           | Temporary conversation context        |
| **Isolation** | `<appName, userID>`              | `<appName, userID, sessionID>`        |
| **Lifecycle** | Persists across sessions         | Valid within a single session         |
| **Content**   | User profile, preferences, facts | Conversation history, messages        |
| **Data Size** | Small (tens to hundreds)         | Large (tens to thousands of messages) |
| **Use Case**  | "Remember who the user is"       | "Remember what was said"              |

**Example**:

```go
// Memory: persists across sessions
memory.AddMemory(ctx, userKey, "User is a backend engineer", []string{"occupation"})

// Session: valid only within a session
session.AddMessage(ctx, sessionKey, userMessage("What's the weather today?"))
session.AddMessage(ctx, sessionKey, agentMessage("It's sunny today"))

// New session: Memory retained, Session reset
```

## Memory ID Idempotency

Memory ID is generated from a SHA256 hash of memory content, appName, userID,
and canonical episodic metadata. Topics are not part of identity, so the same
content for the same user keeps the same ID even if topics change:

```go
// First add
memory.AddMemory(ctx, userKey, "User likes programming", []string{"hobby"})
// Generated ID: abc123...

// Second add with same content and different topics
memory.AddMemory(ctx, userKey, "User likes programming", []string{"interests"})
// Same ID: abc123..., overwrites, refreshes topics and updated_at
```

**Implications**:

- ✅ **Natural deduplication**: Avoids redundant storage
- ✅ **Idempotent operations**: Repeated additions don't create multiple records
- ⚠️ **Overwrite update**: Cannot append same content (add timestamp or sequence number if append is needed)

## Search Behavior Notes

Search behavior depends on the backend:

- For `inmemory` / `redis` / `mysql` / `postgres`: `SearchMemories` uses **BM25-style lexical matching** (not semantic search).
- For `pgvector` / `mysqlvec` / `sqlitevec`: `SearchMemories` uses **vector similarity search** and requires an embedder.
- For `chromadb`: `SearchMemories` uses ChromaDB vector search and supports kind fallback and hybrid search.

**Lexical matching details** (non-vector backends):

**English tokenization**: lowercase → filter stopwords (a, the, is, etc.) → split by spaces

```go
// Can find
Memory: "User likes programming"
Search: "programming" ✅ Match

// Cannot find
Memory: "User likes programming"
Search: "coding" ❌ No match (semantically similar but different words)
```

**Chinese tokenization**: prefers `gse` word segmentation with
low-weight CJK character trigram fallback

```go
Memory: "用户喜欢编程"
Search: "编程" ✅ Match (word-level hit)
Search: "写代码" ❌ No match (different words)
```

**Limitations** (non-vector backends):

- These backends perform filtering and sorting in **application layer** (\[O(n)\] complexity)
- Performance affected by data volume
- Not semantic similarity search
- Ranking uses **BM25-style lexical scoring + query coverage + ordered
  phrase bonus**, not vector semantics

**Recommendations**:

- Use explicit keywords and topic tags to improve hit rate
- If you need semantic similarity search, use the pgvector, mysqlvec, sqlitevec, or ChromaDB backend

## Soft Delete Considerations

**Support status**:

- ✅ MySQL, MySQL Vec, PostgreSQL, pgvector, SQLite, SQLiteVec, ChromaDB: support soft delete
- ❌ InMemory, Redis: not supported (hard delete only)

**Soft delete configuration**:

```go
mysqlService, err := memorymysql.NewService(
    memorymysql.WithMySQLClientDSN("..."),
    memorymysql.WithSoftDelete(true), // Enable soft delete
)
```

**Behavior differences**:

| Operation | Hard Delete       | Soft Delete                              |
| --------- | ----------------- | ---------------------------------------- |
| Delete    | Immediate removal | Set `deleted_at` field                   |
| Query     | Not visible       | Auto-filtered (WHERE deleted_at IS NULL) |
| Recovery  | Cannot recover    | Re-add or rotate an update to the same ID |
| Storage   | Saves space       | Occupies space                           |

**Migration trap**:

```go
// ⚠️ Migrating from soft-delete backend to non-supporting backend
// Soft-deleted records will be lost!

// Migrating from MySQL (soft delete) to Redis (hard delete)
// Need to manually handle soft-deleted records
```
