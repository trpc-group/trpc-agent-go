# Reranker

> **Example Code**: [examples/knowledge/reranker](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/reranker)

Reranker reorders retrieved candidates to improve relevance. It can work with any vector store and is injected via `knowledge.WithReranker`.

## Supported Rerankers

### TopK (simple truncation)
The most basic reranker that returns the top K results based on retrieval scores.

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/topk"

rerank := topk.New(
    topk.WithK(3), // return top 3 results (default: -1, return all)
)
```

| Option | Description | Required |
|--------|-------------|----------|
| `WithK(int)` | Number of results to return, default `-1` (return all) | No |

### Cohere (SaaS rerank)
Use Cohere's rerank service.

```go
import (
    "log"

    "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/cohere"
)

rerank, err := cohere.New(
    cohere.WithAPIKey("your-api-key"),       // required: Cohere API Key
    cohere.WithModel("rerank-english-v3.0"), // model name (default: rerank-english-v3.0)
    cohere.WithTopN(5),                      // number of results to return
    cohere.WithEndpoint("https://..."),      // optional: custom endpoint (default: https://api.cohere.ai/v1/rerank)
    cohere.WithHTTPClient(customClient),     // optional: custom HTTP client
)
if err != nil {
    log.Fatalf("Failed to create reranker: %v", err)
}
```

| Option | Description | Required |
|--------|-------------|----------|
| `WithAPIKey(string)` | Cohere API Key | Yes |
| `WithModel(string)` | Model name, default `rerank-english-v3.0` | No |
| `WithTopN(int)` | Number of results to return | No |
| `WithEndpoint(string)` | Custom endpoint URL | No |
| `WithHTTPClient(*http.Client)` | Custom HTTP client | No |

### Infinity / TEI (standard rerank API)

**Terminology**

- **Infinity**: Open-source high-performance inference engine supporting multiple Reranker models
- **TEI (Text Embeddings Inference)**: Official Hugging Face inference engine optimized for Embeddings and Reranking

The Infinity Reranker implementation in trpc-agent-go can connect to any service compatible with the standard Rerank API, including self-hosted services using Infinity/TEI, Hugging Face Inference Endpoints, etc.

**Usage**

```go
import (
    "log"

    "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker/infinity"
)

rerank, err := infinity.New(
    infinity.WithEndpoint("http://localhost:7997/rerank"), // required: service endpoint
    infinity.WithModel("BAAI/bge-reranker-v2-m3"),         // optional: model name
    infinity.WithTopN(5),                                  // optional: number of results (default: -1, return all)
    infinity.WithAPIKey("your-api-key"),                   // optional: API key for authenticated services
    infinity.WithHTTPClient(customClient),                 // optional: custom HTTP client
)
if err != nil {
    log.Fatalf("Failed to create reranker: %v", err)
}
```

| Option | Description | Required |
|--------|-------------|----------|
| `WithEndpoint(string)` | Service endpoint URL | Yes |
| `WithModel(string)` | Model name | No |
| `WithTopN(int)` | Number of results to return, default `-1` (return all) | No |
| `WithAPIKey(string)` | API key for authenticated services | No |
| `WithHTTPClient(*http.Client)` | Custom HTTP client | No |

For detailed deployment methods and examples, see the [examples/knowledge/reranker/infinity/](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/reranker/infinity) directory.

## Inject into Knowledge

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge"

kb := knowledge.New(
    knowledge.WithReranker(rerank),
    // ... other configurations (VectorStore, Embedder, Sources, etc.)
)
```

## Notes
- Cohere requires a valid API key.
- Infinity/TEI requires a reachable endpoint and a loaded model; `WithModel` is optional but should match the service model when set.
- TopK has no external dependencies and suits offline or constrained environments.
