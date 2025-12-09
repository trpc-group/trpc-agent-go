# PostgreSQL (PGVector) Example

Demonstrates persistent vector storage using PostgreSQL with pgvector extension.

## Prerequisites

1. Install PostgreSQL with pgvector extension:

```bash
# Docker setup (recommended)
docker run -d \
  --name postgres-pgvector \
  -e POSTGRES_PASSWORD=yourpassword \
  -e POSTGRES_DB=vectordb \
  -p 5432:5432 \
  pgvector/pgvector:pg16
```

2. Set environment variables:

```bash
export OPENAI_BASE_URL=xxx
export OPENAI_API_KEY=xxx
export MODEL_NAME=xxx
export PGVECTOR_HOST=127.0.0.1
export PGVECTOR_PORT=5432
export PGVECTOR_USER=postgres
export PGVECTOR_PASSWORD=yourpassword
export PGVECTOR_DATABASE=vectordb
```

## Run

```bash
go run main.go
```

## Benefits

- **Persistent storage**: Embeddings survive restarts
- **Scalable**: Production-ready database
- **Query optimization**: Built-in vector similarity search
- **Data integrity**: ACID transactions

## Reuse embeddings

Run the program multiple times - it will reuse existing embeddings instead of regenerating them!
