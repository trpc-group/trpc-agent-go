# Knowledge Examples

Knowledge-enhanced AI agents examples.

## Environment

```bash
export OPENAI_BASE_URL=xxx
export OPENAI_API_KEY=xxx
export MODEL_NAME=xxx
```

### Common flags

Most examples accept:

```bash
-vectorstore inmemory|pgvector|tcvector|elasticsearch  # default: inmemory
```

## Examples

### basic/
Basic example with file source and in-memory vector store.

### sources/
Different data source types:

| Case | Description |
|------|-------------|
| file-source/ | Load individual files (Markdown, PDF, DOCX, CSV, JSON) |
| directory-source/ | Recursively load entire directory |
| url-source/ | Fetch and parse web pages |
| auto-source/ | Auto-detect source type from path |

### vectorstores/
Persistent vector storage options:

| Case | Description | Extra Environment |
|------|-------------|-------------------|
| postgres/ | PostgreSQL with pgvector extension | `PGVECTOR_HOST`, `PGVECTOR_PORT`, `PGVECTOR_USER`, `PGVECTOR_PASSWORD`, `PGVECTOR_DATABASE`, `PGVECTOR_TABLE` |
| elasticsearch/ | Elasticsearch (v7/v8/v9) | `ELASTICSEARCH_HOSTS`, `ELASTICSEARCH_USERNAME`, `ELASTICSEARCH_PASSWORD`, `ELASTICSEARCH_INDEX_NAME` |
| tcvector/ | Tencent VectorDB | `TCVECTOR_URL`, `TCVECTOR_USERNAME`, `TCVECTOR_PASSWORD`, `TCVECTOR_COLLECTION` |
| milvus/ | Milvus vector database | `MILVUS_ADDRESS`, `MILVUS_USERNAME`, `MILVUS_PASSWORD`, `MILVUS_DB_NAME`, `MILVUS_COLLECTION` |

### features/
Advanced features:

| Case | Description |
|------|-------------|
| agentic-filter/ | LLM automatically generates metadata filter based on user query |
| metadata-filter/ | Programmatic metadata filtering with AND/OR/NOT operations |
| management/ | Dynamic source management: AddSource, RemoveSource, ReloadSource |
