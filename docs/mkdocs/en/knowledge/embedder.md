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

OpenAI Embedder is the most common choice, supporting OpenAI official services and third-party services compatible with OpenAI API:

```go
import (
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
)

// OpenAI Embedder configuration
embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"), // embedding model, can also be set via OPENAI_EMBEDDING_MODEL env var
)

// Pass to Knowledge
kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
)
```

## Gemini Embedder

```go
import (
    geminiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/gemini"
)

embedder, err := geminiembedder.New(context.Background())
if err != nil {
    log.Fatalf("Failed to create gemini embedder: %v", err)
}
```

## Ollama Embedder

```go
import (
    ollamaembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/ollama"
)

embedder, err := ollamaembedder.New()
if err != nil {
    log.Fatalf("Failed to create ollama embedder: %v", err)
}
```

## HuggingFace Embedder

```go
import (
    huggingfaceembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/huggingface"
)

embedder := huggingfaceembedder.New()
```

## Environment Variables

```bash
# OpenAI API configuration (required when using OpenAI embedder, auto-read by OpenAI SDK)
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"

# Google Gemini API configuration (when using Gemini embedder)
export GOOGLE_API_KEY="your-google-api-key"
```
