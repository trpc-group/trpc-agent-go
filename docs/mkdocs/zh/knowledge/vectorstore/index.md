# 向量存储 (VectorStore)

> **示例代码**: [examples/knowledge/vectorstores](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores)

向量存储是 Knowledge 系统的核心组件，负责存储和检索文档的向量表示。

## 支持的向量存储

trpc-agent-go 支持多种向量存储实现：

| 向量存储 | 说明 |
|---------|------|
| [Memory](inmemory.md) | 内存向量存储 |
| [PGVector](pgvector.md) | PostgreSQL + pgvector 扩展 |
| [TcVector](tcvector.md) | 腾讯云向量数据库 |
| [Elasticsearch](elasticsearch.md) | 支持 v7/v8/v9 多版本 |
| [Qdrant](qdrant.md) | 高性能向量数据库 |
| [Milvus](milvus.md) | 高性能向量数据库 |

## 搜索模式

向量存储支持四种搜索模式，系统会根据查询内容**自动选择**最合适的模式：

| 搜索模式 | 枚举值 | 说明 | 特殊要求 |
|---------|-------|------|---------|
| **Vector** | `SearchModeVector` | 语义相似度搜索，理解查询意图 | 需要 Embedder |
| **Keyword** | `SearchModeKeyword` | 关键词精确匹配，适合专业术语 | PGVector 需启用 `WithEnableTSVector(true)` |
| **Hybrid** | `SearchModeHybrid` | 混合向量和关键词（推荐，默认） | PGVector 需启用 `WithEnableTSVector(true)` |
| **Filter** | `SearchModeFilter` | 仅按元数据过滤，不计算相似度 | - |

**示例**:
```go
// Default automatic search mode selection
results, err := kb.Search(ctx, "Large language model applications")

// Filter by metadata
filter := searchfilter.NewFilter(
    searchfilter.WithMetadata("category", "technical docs"),
)
results, err := kb.Search(ctx, "", knowledge.WithSearchFilter(filter))
```

## 过滤器支持

所有向量存储都支持过滤器功能，包括 ID 过滤、元数据过滤和复杂条件过滤（`FilterCondition`）。

## 更多内容

- [Memory](inmemory.md) - 内存向量存储配置
- [PGVector](pgvector.md) - PostgreSQL + pgvector 配置
- [TcVector](tcvector.md) - 腾讯云向量数据库配置
- [Elasticsearch](elasticsearch.md) - Elasticsearch 配置
- [Qdrant](qdrant.md) - Qdrant 配置
- [Milvus](milvus.md) - Milvus 配置
