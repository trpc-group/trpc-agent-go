# Knowledge Examples

Knowledge-enhanced AI agents examples.

## Environment

```bash
export OPENAI_BASE_URL=xxx
export OPENAI_API_KEY=xxx
export MODEL_NAME=xxx
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
| postgres/ | PostgreSQL with pgvector extension | `PGVECTOR_HOST`, `PGVECTOR_PORT`, `PGVECTOR_USER`, `PGVECTOR_PASSWORD`, `PGVECTOR_DATABASE` |
| elasticsearch/ | Elasticsearch (v7/v8/v9) | `ELASTICSEARCH_HOSTS`, `ELASTICSEARCH_USERNAME`, `ELASTICSEARCH_PASSWORD` |
| tcvector/ | Tencent VectorDB | `TCVECTOR_URL`, `TCVECTOR_USERNAME`, `TCVECTOR_PASSWORD` |

### features/
Advanced features:

| Case | Description |
|------|-------------|
| agentic-filter/ | LLM automatically generates metadata filter based on user query |
| metadata-filter/ | Programmatic metadata filtering with AND/OR/NOT operations |
| management/ | Dynamic source management: AddSource, RemoveSource, ReloadSource |
