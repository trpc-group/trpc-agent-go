# Elasticsearch

> **Example Code**: [examples/knowledge/vectorstores/elasticsearch](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/vectorstores/elasticsearch)

Elasticsearch vector store supports v7, v8, v9 versions.

## Basic Configuration

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    vectorelasticsearch "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
)

// Create Elasticsearch vector store supporting multiple versions (v7, v8, v9)
esVS, err := vectorelasticsearch.New(
    vectorelasticsearch.WithAddresses([]string{"http://localhost:9200"}),
    vectorelasticsearch.WithUsername("your-username"),
    vectorelasticsearch.WithPassword("your-password"),
    vectorelasticsearch.WithIndexName("trpc_agent_documents"),
    // Version options: "v7", "v8", "v9" (default "v9")
    vectorelasticsearch.WithVersion("v9"),
)
if err != nil {
    // Handle error
}

kb := knowledge.New(
    knowledge.WithVectorStore(esVS),
    knowledge.WithEmbedder(embedder),
)
```

## Configuration Options

### Connection Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithAddresses(addresses)` | Elasticsearch service address list | `["http://localhost:9200"]` |
| `WithUsername(username)` | Username | - |
| `WithPassword(password)` | Password | - |
| `WithAPIKey(apiKey)` | API Key authentication | - |
| `WithCertificateFingerprint(fp)` | Certificate fingerprint authentication | - |
| `WithVersion(version)` | ES version (v7/v8/v9) | `"v9"` |

### Index Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithIndexName(name)` | Index name | `"trpc_agent_documents"` |
| `WithVectorDimension(dim)` | Vector dimension | `1536` |
| `WithEnableTSVector(enabled)` | Enable text search vector | `true` |

### Search Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithMaxResults(n)` | Default number of search results | `10` |
| `WithScoreThreshold(threshold)` | Minimum similarity score threshold | `0.7` |

### Advanced Configuration

| Option | Description | Default |
|--------|-------------|---------|
| `WithMaxRetries(n)` | Maximum retry count | `3` |
| `WithCompressRequestBody(enabled)` | Enable request compression | `true` |
| `WithEnableMetrics(enabled)` | Enable metrics collection | `false` |
| `WithEnableDebugLogger(enabled)` | Enable debug logging | `false` |
| `WithRetryOnStatus(codes)` | HTTP status codes to retry on | `[500, 502, 503, 429]` |
| `WithDocBuilder(builder)` | Custom document builder method | Default builder |
| `WithExtraOptions(opts...)` | Inject custom ES ClientBuilder config, not needed by default | - |

### Field Mapping (Advanced)

| Option | Description | Default |
|--------|-------------|---------|
| `WithIDField(field)` | ID field name | `"id"` |
| `WithNameField(field)` | Name field name | `"name"` |
| `WithContentField(field)` | Content field name | `"content"` |
| `WithEmbeddingField(field)` | Vector field name | `"embedding"` |
| `WithMetadataField(field)` | Metadata field name | `"metadata"` |
| `WithCreatedAtField(field)` | Created time field name | `"created_at"` |
| `WithUpdatedAtField(field)` | Updated time field name | `"updated_at"` |

## Version Selection

Select the corresponding configuration based on your Elasticsearch service version:

```go
// Elasticsearch 7.x
vectorelasticsearch.WithVersion("v7")

// Elasticsearch 8.x
vectorelasticsearch.WithVersion("v8")

// Elasticsearch 9.x (default)
vectorelasticsearch.WithVersion("v9")
```
