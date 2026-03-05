# Vector Store

> **Example Code**: [examples/knowledge/vectorstores](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores)

Vector store is the core component of the Knowledge system, responsible for storing and retrieving vector representations of documents.

## Supported Vector Stores

trpc-agent-go supports multiple vector store implementations:

| Vector Store | Description |
|--------------|-------------|
| [Memory](inmemory.md) | In-memory vector store |
| [PGVector](pgvector.md) | PostgreSQL + pgvector extension |
| [TcVector](tcvector.md) | Tencent Cloud vector database |
| [Elasticsearch](elasticsearch.md) | Supports v7/v8/v9 versions |
| [Qdrant](qdrant.md) | High-performance vector database |
| [Milvus](milvus.md) | High-performance vector database |

## Search Modes

Vector stores support four search modes. The system **automatically selects** the most appropriate mode based on the query:

| Search Mode | Enum Value | Description | Requirements |
|-------------|------------|-------------|--------------|
| **Vector** | `SearchModeVector` | Semantic similarity search, understands query intent | Requires Embedder |
| **Keyword** | `SearchModeKeyword` | Exact keyword matching, suitable for technical terms | PGVector requires `WithEnableTSVector(true)` |
| **Hybrid** | `SearchModeHybrid` | Combines vector and keyword search (recommended, default) | PGVector requires `WithEnableTSVector(true)` |
| **Filter** | `SearchModeFilter` | Metadata-only filtering, no similarity score | - |

**Examples**:
```go
// Default automatic search
reqSearch := &knowledge.SearchRequest{
    Query: "Large language model applications",
    //History: nil,
    //UserID: "",
    //SessionID: "",
    //MaxResults: 0,
    //MinScore: 0,
    //SearchFilter: nil,
    //SearchMode: 0,
}
rspSearch, err := kb.Search(ctx, reqSearch)
if err != nil {
    log.Debugf("Failed to search knowledge base: %v", err)
    return
}
log.Infof("Search Result:[%+v]", rspSearch)
for i, r := range rspSearch.Documents {
    log.Infof("rspSearch.DOC_%d.score=%v,doc:[%+v]", i, r.Score, r.Document)
}

// Search with filter conditions
filter := knowledge.SearchFilter{
    //DocumentIDs: nil,
    //Metadata: nil,
    FilterCondition: searchfilter.And(
        searchfilter.Equal("category", "film"),
        searchfilter.Equal("country_code", "us"),
    ),
}
reqSearchWithFilter := &knowledge.SearchRequest{
    Query:        "Top ranked movie",
    SearchFilter: &filter,
}
rspSearchWithFilter, err := kb.Search(ctx, reqSearchWithFilter)
if err != nil {
    log.Debugf("Failed to search knowledge base: %v", err)
    return
}
log.Infof("Search Result:[%+v]", rspSearchWithFilter)
for i, r := range rspSearchWithFilter.Documents {
    log.Infof("rspSearchWithFilter.DOC_%d.score=%v,doc:[%+v]", i, r.Score, r.Document)
}
```

## Filter Support

All vector stores support filter functionality, including ID filtering, metadata filtering, and complex condition filtering (`FilterCondition`).

## More Content

- [Memory](inmemory.md) - In-memory vector store configuration
- [PGVector](pgvector.md) - PostgreSQL + pgvector configuration
- [TcVector](tcvector.md) - Tencent Cloud vector database configuration
- [Elasticsearch](elasticsearch.md) - Elasticsearch configuration
- [Qdrant](qdrant.md) - Qdrant configuration
- [Milvus](milvus.md) - Milvus configuration
