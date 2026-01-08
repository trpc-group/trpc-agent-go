# Memory (In-Memory Vector Store)

> **Example Code**: [examples/knowledge/vectorstores/inmemory](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/inmemory)

In-memory vector store is the simplest implementation, suitable for development testing and small-scale data scenarios.

## Basic Configuration

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

memVS := vectorinmemory.New()

kb := knowledge.New(
    knowledge.WithVectorStore(memVS),
    knowledge.WithEmbedder(embedder),
)
```

## Configuration Options

| Option | Description | Default |
|--------|-------------|---------|
| `WithMaxResults(n)` | Default number of search results | `10` |

## Features

- ✅ Zero configuration, works out of the box
- ✅ Supports all filter functionality (including FilterCondition)
- ⚠️ Data not persisted, lost after restart
- ⚠️ Only suitable for development and testing environments

## Search Modes

| Mode | Support | Description |
|------|---------|-------------|
| Vector | ✅ | Vector similarity search (cosine similarity) |
| Filter | ✅ | Filter-only search, sorted by creation time |
| Hybrid | ⚠️ | Falls back to vector search |
| Keyword | ⚠️ | Falls back to filter search |
