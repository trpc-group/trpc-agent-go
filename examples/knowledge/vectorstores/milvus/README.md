# Milvus Vector Store Example

Demonstrates using Milvus for high-performance vector storage and similarity search.

## Prerequisites

1. Start Milvus:

```bash
# Docker setup (recommended)
# Download docker-compose.yml
wget https://github.com/milvus-io/milvus/releases/download/v2.5.0/milvus-standalone-docker-compose.yml -O docker-compose.yml

# Start Milvus
docker-compose up -d
```

2. Set environment variables:

```bash
export OPENAI_BASE_URL=xxx
export OPENAI_API_KEY=xxx
export MODEL_NAME=xxx
export MILVUS_ADDRESS=localhost:19530
export MILVUS_USERNAME=root
export MILVUS_PASSWORD=Milvus
export MILVUS_DB_NAME=test
export MILVUS_COLLECTION=trpc_example
```

## Run

```bash
go run main.go
```

## Features

- **High performance**: Purpose-built for billion-scale vector similarity search
- **Hybrid search**: Supports dense vector, sparse vector, and full-text search
- **Multiple index types**: IVF, HNSW, DiskANN, and more
- **GPU acceleration**: Optional GPU support for faster indexing and search
- **Cloud-native**: Kubernetes-native architecture with horizontal scaling
- **Persistent storage**: Embeddings survive restarts with durable storage
