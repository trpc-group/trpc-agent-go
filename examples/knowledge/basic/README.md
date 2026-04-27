# Basic Knowledge Example

This is the simplest example to get started with knowledge-enhanced chat using trpc-agent-go.

## What it demonstrates

- Single file source
- In-memory vector store by default
- OpenAI embedder
- Basic question answering
- Command-line query option

## Prerequisites

Set your OpenAI API key:

```bash
export OPENAI_API_KEY=your-api-key
export OPENAI_BASE_URL=https://api.openai.com/v1  # Optional
export MODEL_NAME=deepseek-v4-flash                    # Optional, defaults to deepseek-v4-flash
```

## Run

### With default query

```bash
go run main.go
```

### With custom query

```bash
go run main.go -query "What is a Large Language Model?"
```

### Choose vector store

By default, this example uses `inmemory`, which requires no extra vector store configuration:

```bash
go run main.go
```

You can also switch to another vector store when needed:

```bash
go run main.go -vectorstore inmemory
go run main.go -vectorstore sqlitevec
go run main.go -vectorstore pgvector
go run main.go -vectorstore tcvector
go run main.go -vectorstore elasticsearch
```

Non-default vector stores may require additional backend-specific configuration.

Backend environment variables:

- `sqlitevec`: `SQLITEVEC_DSN`, `SQLITEVEC_TABLE`, `SQLITEVEC_METADATA_TABLE`
- `pgvector`: `PGVECTOR_HOST`, `PGVECTOR_PORT`, `PGVECTOR_USER`, `PGVECTOR_PASSWORD`, `PGVECTOR_DATABASE`, `PGVECTOR_TABLE`
- `tcvector`: `TCVECTOR_URL`, `TCVECTOR_USERNAME`, `TCVECTOR_PASSWORD`, `TCVECTOR_COLLECTION`
- `elasticsearch`: `ELASTICSEARCH_HOSTS`, `ELASTICSEARCH_USERNAME`, `ELASTICSEARCH_PASSWORD`, `ELASTICSEARCH_INDEX_NAME`, `ELASTICSEARCH_VERSION`

Examples:

`inmemory` does not require any vector-store-specific environment variables:

```bash
go run main.go -vectorstore inmemory
```

`sqlitevec` can be configured with a local SQLite file:

```bash
export SQLITEVEC_DSN='file:knowledge.sqlite?_busy_timeout=5000'
go run main.go -vectorstore sqlitevec
```

`pgvector` can be configured with PostgreSQL connection settings:

```bash
export PGVECTOR_HOST=127.0.0.1
export PGVECTOR_PORT=5432
export PGVECTOR_USER=root
export PGVECTOR_PASSWORD=
export PGVECTOR_DATABASE=vectordb
export PGVECTOR_TABLE=trpc_agent_go
go run main.go -vectorstore pgvector
```

`tcvector` requires the service endpoint and credentials:

```bash
export TCVECTOR_URL=http://127.0.0.1:8080
export TCVECTOR_USERNAME=your-username
export TCVECTOR_PASSWORD=your-password
export TCVECTOR_COLLECTION=trpc_agent_go
go run main.go -vectorstore tcvector
```

`elasticsearch` can be configured with index and auth settings:

```bash
export ELASTICSEARCH_HOSTS=http://localhost:9200
export ELASTICSEARCH_USERNAME=
export ELASTICSEARCH_PASSWORD=
export ELASTICSEARCH_INDEX_NAME=trpc_agent_go
export ELASTICSEARCH_VERSION=v8
go run main.go -vectorstore elasticsearch
```

## Example questions

- "What is a Large Language Model?"
- "How do LLMs work?"
- "What are transformers?"

## Next steps

- **sources/**: Learn about different data sources (file, directory, URL, auto)
- **vectorstores/**: Explore persistent storage options (PostgreSQL, Elasticsearch)
- **features/**: Discover advanced features (agentic filter, streaming, metadata)
