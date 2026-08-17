# SQLiteVec（sqlite-vec）存储

通用的 Agent 接入、提取模式和工具配置请参阅[使用与配置](usage.md)。

**适用场景**：本地持久化 + 语义检索（单机）

SQLiteVec 将记忆保存在 SQLite 文件中，并通过 `sqlite-vec` 提供向量相似度
检索（语义检索）。相比普通 SQLite 后端，它需要配置 **embedder** 来为
记忆和查询生成 embedding。

```go
import (
    "database/sql"

    _ "github.com/mattn/go-sqlite3"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    memorysqlitevec "trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec"
)

db, err := sql.Open("sqlite3", "file:memories_vec.db?_busy_timeout=5000")
if err != nil {
    panic(err)
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
    _ = db.Close()
    panic(err)
}
defer memoryService.Close()
```

**配置选项**：

- `WithTableName(name)`: 表名（默认 "memories"）
- `WithEmbedder(embedder)`: 文本 embedder（必填）
- `WithIndexDimension(dim)`: 向量维度（默认与 embedder 维度一致）
- `WithMaxResults(limit)`: 搜索返回的最大条数（默认 10）
- `WithSoftDelete(enabled)`: 软删除（默认 false）
- `WithMemoryLimit(limit)`: 每用户记忆上限
- `WithSkipDBInit(skip)`: 跳过表初始化
- Auto 模式：`WithExtractor`、`WithAsyncMemoryNum`、`WithMemoryQueueSize`、
  `WithMemoryJobTimeout`
- 工具：`WithCustomTool`、`WithToolEnabled`

**注意事项**：

- 该后端使用 `github.com/mattn/go-sqlite3`，需要 CGO。
- `sqlite-vec` 扩展通过 Go 绑定在进程内编译与注册，运行时无需额外下载
  `.so/.dylib` 文件。
