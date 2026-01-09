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

## 过滤器支持

所有向量存储都支持过滤器功能，包括 ID 过滤、元数据过滤和复杂条件过滤（`FilterCondition`）。

## 更多内容

- [Memory](inmemory.md) - 内存向量存储配置
- [PGVector](pgvector.md) - PostgreSQL + pgvector 配置
- [TcVector](tcvector.md) - 腾讯云向量数据库配置
- [Elasticsearch](elasticsearch.md) - Elasticsearch 配置
- [Qdrant](qdrant.md) - Qdrant 配置
- [Milvus](milvus.md) - Milvus 配置
