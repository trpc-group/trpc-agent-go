# SQLiteVec (sqlite-vec) Storage

For common Agent integration, extraction modes, and tool configuration, see
[Usage and Configuration](usage.md).

**Use case**: Local persistence + semantic memory search on a single node

SQLiteVec stores memories in a SQLite file and uses `sqlite-vec` to do
vector similarity search (semantic search). Compared to the plain SQLite
backend, it requires an **embedder** to generate embeddings.

```go
import (
    "database/sql"

    _ "github.com/mattn/go-sqlite3"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    memorysqlitevec "trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec"
)

db, err := sql.Open("sqlite3", "file:memories_vec.db?_busy_timeout=5000")
if err != nil {
    // handle error
}

emb := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
)

memoryService, err := memorysqlitevec.NewService(
    db,
    memorysqlitevec.WithEmbedder(emb),
    memorysqlitevec.WithSoftDelete(true),
    memorysqlitevec.WithMemoryLimit(200),
)
if err != nil {
    // handle error
}
defer memoryService.Close()
```

**Configuration options**:

- `WithTableName(name)`: Table name (default "memories")
- `WithEmbedder(embedder)`: Text embedder for vector generation (required)
- `WithIndexDimension(dim)`: Vector dimension (default is embedder dimension)
- `WithMaxResults(limit)`: Max search results (default 10)
- `WithSoftDelete(enabled)`: Enable soft delete (default false)
- `WithMemoryLimit(limit)`: Memory limit per user
- `WithSkipDBInit(skip)`: Skip table initialization
- Auto mode: `WithExtractor`, `WithAsyncMemoryNum`, `WithMemoryQueueSize`,
  `WithMemoryJobTimeout`
- Tools: `WithCustomTool`, `WithToolEnabled`

**Notes**:

- This backend uses `github.com/mattn/go-sqlite3` and requires CGO.
- The `sqlite-vec` extension is compiled and registered in-process via Go
  bindings (no external `.so/.dylib` download at runtime).
