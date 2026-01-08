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

## Filter Support

All vector stores support filter functionality, including ID filtering, metadata filtering, and complex condition filtering (`FilterCondition`).

## More Content

- [Memory](inmemory.md) - In-memory vector store configuration
- [PGVector](pgvector.md) - PostgreSQL + pgvector configuration
- [TcVector](tcvector.md) - Tencent Cloud vector database configuration
- [Elasticsearch](elasticsearch.md) - Elasticsearch configuration
- [Qdrant](qdrant.md) - Qdrant configuration
- [Milvus](milvus.md) - Milvus configuration
