# Embedder

Embedder is responsible for converting text into vector representations and is a core component of the Knowledge system.

## Supported Embedding Models

trpc-agent-go supports multiple embedding model platforms:

| Platform | Package Path | Description |
|----------|--------------|-------------|
| OpenAI | `knowledge/embedder/openai` | OpenAI embedding models (text-embedding-3-small, etc.), supports all platforms compatible with OpenAI Embedding API |
| Gemini | `knowledge/embedder/gemini` | Google Gemini embedding models |
| Ollama | `knowledge/embedder/ollama` | Local Ollama embedding models, suitable for offline deployment |
| HuggingFace | `knowledge/embedder/huggingface` | HuggingFace Text Embedding Inference models |

## OpenAI Embedder

OpenAI Embedder is the most common choice, supporting OpenAI official services and third-party services compatible with OpenAI API.

**Environment Variable Configuration**

OpenAI Embedder automatically reads configuration from environment variables, eliminating the need to hardcode sensitive information in code:

*   `OPENAI_API_KEY`: API Key (if `WithAPIKey` is not provided in code, this env var is read automatically)
*   `OPENAI_BASE_URL`: Base URL (optional, for third-party services compatible with OpenAI API)

**Code Example**

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
)

// OpenAI Embedder configuration
embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"), // Default model
)

// Pass to Knowledge
kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
)
```

**Configuration Options**

| Option | Description | Default Value |
|--------|-------------|---------------|
| `WithModel(model string)` | Set embedding model | `text-embedding-3-small` |
| `WithDimensions(dim int)` | Set vector dimensions (only valid for text-embedding-3 series) | `1536` |
| `WithEncodingFormat(fmt string)` | Set encoding format ("float", "base64") | `"float"` |
| `WithUser(user string)` | Set user identifier | - |
| `WithAPIKey(key string)` | Set API Key (prioritized over env var) | - |
| `WithOrganization(org string)` | Set Organization ID (prioritized over env var) | - |
| `WithBaseURL(url string)` | Set Base URL | - |
| `WithRequestOptions(opts...)` | Set additional request options | - |
| `WithMaxRetries(n int)` | Set max retry count | `2` |
| `WithRetryBackoff(durations)` | Set retry backoff durations (wait time for each retry). If retry count exceeds backoff array length, remaining retries use the last value | `[100ms, 200ms, 400ms, 800ms]` |

## Gemini Embedder

Google Gemini Embedder uses Google GenAI SDK.

**Environment Variable Configuration**

*   `GOOGLE_API_KEY`: API Key (if `WithAPIKey` is not provided in code, this env var is read automatically)

**Code Example**

```go
import (
    "context"
    "log"

    geminiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/gemini"
)

embedder, err := geminiembedder.New(context.Background())
if err != nil {
    log.Fatalf("Failed to create gemini embedder: %v", err)
}
```

**Configuration Options**

| Option | Description | Default Value |
|--------|-------------|---------------|
| `WithModel(model string)` | Set model | `gemini-embedding-exp-03-07` |
| `WithDimensions(dim int)` | Set dimensions | `1536` |
| `WithTaskType(type string)` | Set task type (e.g. `RETRIEVAL_QUERY`) | `RETRIEVAL_QUERY` |
| `WithTitle(title string)` | Set title (only when TaskType is `RETRIEVAL_DOCUMENT`) | - |
| `WithAPIKey(key string)` | Set API Key (prioritized over env var) | - |
| `WithRole(role string)` | Set role | `user` |
| `WithClientOptions(opts)` | Set GenAI client options | - |
| `WithRequestOptions(opts)` | Set GenAI request options | - |

## Ollama Embedder

Locally running Ollama models.

**Environment Variable Configuration**

*   `OLLAMA_HOST`: Ollama service address (if `WithHost` is not provided in code, this env var is read automatically, default is `http://localhost:11434`)

**Code Example**

```go
import (
    ollamaembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/ollama"
)

embedder := ollamaembedder.New()
```

**Configuration Options**

| Option | Description | Default Value |
|--------|-------------|---------------|
| `WithModel(model string)` | Set model | `llama3.2:latest` |
| `WithHost(host string)` | Set Host | `http://localhost:11434` |
| `WithDimensions(dim int)` | Set dimensions | `1536` |
| `WithTruncate(truncate bool)` | Set whether to truncate | - |
| `WithUseEmbeddings()` | Use `/api/embeddings` endpoint | `false` (uses `/api/embed`) |
| `WithOptions(opts map)` | Set Ollama model parameters | - |
| `WithKeepAlive(d Duration)` | Set KeepAlive duration | - |

## HuggingFace Embedder

HuggingFace Text Embedding Inference (TEI) service.

**Code Example**

```go
import (
    huggingfaceembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/huggingface"
)

embedder := huggingfaceembedder.New()
```

**Configuration Options**

| Option | Description | Default Value |
|--------|-------------|---------------|
| `WithBaseURL(url string)` | Set Base URL | `http://localhost:8080` |
| `WithDimensions(dim int)` | Set dimensions | `1024` |
| `WithNormalize(norm bool)` | Whether to normalize | - |
| `WithPromptName(name string)` | Set Prompt Name | - |
| `WithTruncate(trunc bool)` | Whether to truncate | - |
| `WithTruncationDirection(dir)` | Truncation direction (`Left`/`Right`) | `Right` |
| `WithEmbedRoute(route)` | Set route (`/embed` or `/embed_all`) | `/embed` |
| `WithClient(client)` | Set HTTP Client | `http.DefaultClient` |
