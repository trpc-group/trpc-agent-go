# Knowledge Usage Documentation

## Overview

Knowledge is the knowledge management system in the tRPC-Agent-Go framework, providing Retrieval-Augmented Generation (RAG) capabilities for Agents. By integrating vector data, embedding models, and document processing components, the Knowledge system enables Agents to access and retrieve relevant knowledge information, thereby providing more accurate and well-founded responses.

### Usage Pattern

The usage of the Knowledge system follows this pattern:

1. **Create Knowledge**: Configure vector storage, Embedder, and knowledge sources
2. **Load Documents**: Load and index documents from various sources
3. **Integrate with Agent**: Use `WithKnowledge()` to integrate Knowledge into LLM Agent
4. **Agent Auto Retrieval**: Agent automatically performs knowledge retrieval through built-in `knowledge_search` tool

This pattern provides:

- **Intelligent Retrieval**: Semantic search based on vector similarity
- **Multi-source Support**: Support for files, directories, URLs, and other knowledge sources
- **Flexible Storage**: Support for memory, PostgreSQL, TcVector, and other storage backends
- **High Performance Processing**: Concurrent processing and batch document loading
- **Extensible Architecture**: Support for custom Embedders, Retrievers, and Rerankers

### Agent Integration

How the Knowledge system integrates with Agents:

- **Automatic Tool Registration**: Use `WithKnowledge()` option to automatically add `knowledge_search` tool
- **Tool Invocation**: Agents can call knowledge search tools to obtain relevant information
- **Context Enhancement**: Retrieved knowledge content is automatically added to Agent's context

## Quick Start

### Environment Requirements

- Go 1.24.1 or higher
- Valid LLM API key (OpenAI compatible interface)
- Vector database (optional, for production environment)

### Configure Environment Variables

```bash
# OpenAI API configuration.
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"

# Embedding model configuration (optional, needs manual reading).
export OPENAI_EMBEDDING_MODEL="text-embedding-3-small"
```

### Minimal Example

```go
package main

import (
    "context"
    "log"

    // Core components.
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
    ctx := context.Background()

    // 1. Create embedder.
    embedder := openaiembedder.New(
        openaiembedder.WithModel("text-embedding-3-small"),
    )

    // 2. Create vector store.
    vectorStore := vectorinmemory.New()

    // 3. Create knowledge sources (ensure these paths exist or replace with your own paths).
    // The following files are in https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge.
    sources := []source.Source{
        filesource.New([]string{"./data/llm.md"}),
        dirsource.New([]string{"./dir"}),
    }

    // 4. Create Knowledge.
    kb := knowledge.New(
        knowledge.WithEmbedder(embedder),
        knowledge.WithVectorStore(vectorStore),
        knowledge.WithSources(sources),
    )

    // 5. Load documents.
    log.Println("🚀 Starting to load Knowledge ...")
    if err := kb.Load(ctx); err != nil {
        log.Fatalf("Failed to load knowledge base: %v", err)
    }
    log.Println("✅ Knowledge loading completed!")

    // 6. Create LLM model.
    modelInstance := openai.New("claude-4-sonnet-20250514")

    // 7. Create Agent and integrate Knowledge.
    llmAgent := llmagent.New(
        "knowledge-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("Intelligent assistant with Knowledge access capabilities"),
        llmagent.WithInstruction("Use the knowledge_search tool to retrieve relevant information from Knowledge and answer questions based on retrieved content."),
        llmagent.WithKnowledge(kb), // Automatically add knowledge_search tool.
    )

    // 8. Create Runner.
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "knowledge-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
    )

    // 9. Execute conversation (Agent will automatically use knowledge_search tool).
    log.Println("🔍 Starting to search Knowledge ...")
    message := model.NewUserMessage("Please tell me about LLM information")
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }

    // 10. Handle response ...
}
```

## Core Concepts

The [knowledge module](https://github.com/trpc-group/trpc-agent-go/tree/main/knowledge) is the knowledge management core of the tRPC-Agent-Go framework, providing complete RAG capabilities. The module adopts a modular design, supporting multiple document sources, vector storage backends, and embedding models.

```
knowledge/
├── knowledge.go          # Core interface definitions and main implementation.
├── source/               # Document source management.
│   ├── source.go        # Source interface definition.
│   ├── file.go          # File source implementation.
│   ├── dir.go           # Directory source implementation.
│   ├── url.go           # URL source implementation.
│   └── auto.go          # Automatic source type detection.
├── vectorstore/          # Vector storage backend.
│   ├── vectorstore.go   # VectorStore interface definition.
│   ├── inmemory/        # In-memory vector storage (for development/testing).
│   ├── pgvector/        # PostgreSQL + pgvector implementation.
│   └── tcvector/        # Tencent Cloud vector database implementation.
├── embedder/             # Text embedding models.
│   ├── embedder.go      # Embedder interface definition.
│   ├── openai/          # OpenAI embedding model.
│   └── local/           # Local embedding model.
├── document/             # Document representation.
│   └── document.go      # Document structure definition.
├── query/                # Query enhancer.
│   ├── query.go         # QueryEnhancer interface definition.
│   └── passthrough.go   # Default passthrough enhancer.
└── loader/               # Document loader.
    └── loader.go        # Document loading logic.
```

## Usage Guide

### Integration with Agent

Use `llmagent.WithKnowledge(kb)` to integrate Knowledge into Agent. The framework automatically registers the `knowledge_search` tool without needing to manually create custom tools.

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/tool" // Optional: use when adding other tools.
)

// Create Knowledge.
// kb := ...

// Create Agent and integrate Knowledge.
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithDescription("Intelligent assistant with Knowledge access capabilities"),
    llmagent.WithInstruction("Use the knowledge_search tool to retrieve relevant information from Knowledge and answer questions based on retrieved content."),
    llmagent.WithKnowledge(kb), // Automatically add knowledge_search tool.
    // llmagent.WithTools([]tool.Tool{otherTool}), // Optional: add other tools.
)
```

### Vector Store

Vector storage can be configured through options in code. Configuration sources can be configuration files, command line parameters, or environment variables, which users can implement themselves.

#### Vector Store Configuration Examples

```go
import (
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
    vectorpgvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
    vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
    vectorelasticsearch "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
)

// In-memory implementation, can be used for testing.
memVS := vectorinmemory.New()

// PostgreSQL + pgvector.
pgVS, err := vectorpgvector.New(
    vectorpgvector.WithHost("127.0.0.1"),
    vectorpgvector.WithPort(5432),
    vectorpgvector.WithUser("postgres"),
    vectorpgvector.WithPassword("your-password"),
    vectorpgvector.WithDatabase("your-database"),
    // Set index dimension based on embedding model (text-embedding-3-small is 1536).
    pgvector.WithIndexDimension(1536),
    // Enable/disable text retrieval vector, used with hybrid search weights.
    pgvector.WithEnableTSVector(true),
    // Adjust hybrid search weights (vector similarity weight vs text relevance weight).
    pgvector.WithHybridSearchWeights(0.7, 0.3),
    // If Chinese word segmentation extension is installed (like zhparser/jieba), set language to improve text recall.
    pgvector.WithLanguageExtension("english"),
)
if err != nil {
    // Handle error.
}

// TcVector.
tcVS, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-tcvector-endpoint"),
    vectortcvector.WithUsername("your-username"),
    vectortcvector.WithPassword("your-password"),
)
if err != nil {
    // Handle error.
}

// Pass to Knowledge.
kb := knowledge.New(
    knowledge.WithVectorStore(memVS), // pgVS, tcVS.
)
```

#### Elasticsearch

```go
// Create Elasticsearch vector store with multi-version support (v7, v8, v9)
esVS, err := vectorelasticsearch.New(
    vectorelasticsearch.WithAddresses([]string{"http://localhost:9200"}),
    vectorelasticsearch.WithUsername(os.Getenv("ELASTICSEARCH_USERNAME")),
    vectorelasticsearch.WithPassword(os.Getenv("ELASTICSEARCH_PASSWORD")),
    vectorelasticsearch.WithAPIKey(os.Getenv("ELASTICSEARCH_API_KEY")),
    vectorelasticsearch.WithIndexName(getEnvOrDefault("ELASTICSEARCH_INDEX_NAME", "trpc_agent_documents")),
    vectorelasticsearch.WithMaxRetries(3),
    // Version options: "v7", "v8", "v9" (default "v9")
    vectorelasticsearch.WithVersion("v9"),
)
if err != nil {
    // Handle error.
}

kb := knowledge.New(
    knowledge.WithVectorStore(esVS),
)
```

### Embedder

Embedder is responsible for converting text to vector representations and is a core component of the Knowledge system. Currently, the framework mainly supports OpenAI embedding models:

```go
import (
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
)

// OpenAI Embedder configuration.
embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"), // Embedding model, can also be set via OPENAI_EMBEDDING_MODEL environment variable.
)

// Pass to Knowledge.
kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
)
```

**Supported embedding models**:

- OpenAI embedding models (text-embedding-3-small, etc.)
- Other OpenAI API compatible embedding services
- Gemini embedding model (via `knowledge/embedder/gemini`)

> **Note**:
>
> - Retriever and Reranker are currently implemented internally by Knowledge, users don't need to configure them separately. Knowledge automatically handles document retrieval and result ranking.
> - The `OPENAI_EMBEDDING_MODEL` environment variable needs to be manually read in code, the framework won't read it automatically. Refer to the `getEnvOrDefault("OPENAI_EMBEDDING_MODEL", "")` implementation in the example code.

### Document Source Configuration

The source module provides multiple document source types, each supporting rich configuration options:

```go
import (
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
    autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
)

// File source: Single file processing, supports .txt, .md, .go, .json, etc. formats.
fileSrc := filesource.New(
    []string{"./data/llm.md"},
    filesource.WithChunkSize(1000),      // Chunk size.
    filesource.WithChunkOverlap(200),    // Chunk overlap.
    filesource.WithName("LLM Doc"),
    filesource.WithMetadataValue("type", "documentation"),
)

// Directory source: Batch directory processing, supports recursion and filtering.
dirSrc := dirsource.New(
    []string{"./docs"},
    dirsource.WithRecursive(true),                           // Recursively process subdirectories.
    dirsource.WithFileExtensions([]string{".md", ".txt"}),   // File extension filtering.
    dirsource.WithExcludePatterns([]string{"*.tmp", "*.log"}), // Exclusion patterns.
    dirsource.WithChunkSize(800),
    dirsource.WithName("Documentation"),
)

// URL source: Get content from web pages and APIs.
urlSrc := urlsource.New(
    []string{"https://en.wikipedia.org/wiki/Artificial_intelligence"},
    urlsource.WithTimeout(30*time.Second),           // Request timeout.
    urlsource.WithUserAgent("MyBot/1.0"),           // Custom User-Agent.
    urlsource.WithMaxContentLength(1024*1024),       // Maximum content length (1MB).
    urlsource.WithName("Web Content"),
)

// Auto source: Intelligent type recognition, automatically select processor.
autoSrc := autosource.New(
    []string{
        "Cloud computing provides on-demand access to computing resources.",
        "https://docs.example.com/api",
        "./config.yaml",
    },
    autosource.WithName("Mixed Sources"),
    autosource.WithFallbackChunkSize(1000),
)

// Combine usage.
sources := []source.Source{fileSrc, dirSrc, urlSrc, autoSrc}

// Pass to Knowledge.
kb := knowledge.New(
    knowledge.WithSources(sources),
)

// Load all sources.
if err := kb.Load(ctx); err != nil {
    log.Fatalf("Failed to load knowledge base: %v", err)
}
```

### Batch Document Processing and Concurrency

Knowledge supports batch document processing and concurrent loading, which can significantly improve processing performance for large amounts of documents:

```go
err := kb.Load(ctx,
    knowledge.WithShowProgress(true),      // Print progress logs.
    knowledge.WithProgressStepSize(10),    // Progress step size.
    knowledge.WithShowStats(true),         // Print statistics.
    knowledge.WithSourceConcurrency(4),    // Source-level concurrency.
    knowledge.WithDocConcurrency(64),      // Document-level concurrency.
)
```

> Note on performance and rate limits:
>
> - Higher concurrency increases embedder API request rates (OpenAI/Gemini) and may hit rate limits.
> - Tune `WithSourceConcurrency()` and `WithDocConcurrency()` based on throughput, cost, and limits.
> - Defaults are balanced for most scenarios; increase for speed, decrease to avoid throttling.

## Advanced Features

### QueryEnhancer

QueryEnhancer is used to preprocess and optimize user queries before searching. Currently, the framework only provides a default implementation:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/query"
)

kb := knowledge.New(
    knowledge.WithQueryEnhancer(query.NewPassthroughEnhancer()), // Default enhancer, returns query as-is.
)
```

> **Note**: QueryEnhancer is not a required component. If not specified, Knowledge will directly use the original query for search. This option only needs to be configured when custom query preprocessing logic is required.

### Performance Optimization

The Knowledge system provides various performance optimization strategies, including concurrent processing, vector storage optimization, and caching mechanisms:

```go
// Adjust concurrency based on system resources.
kb := knowledge.New(
    knowledge.WithSources(sources),
    knowledge.WithSourceConcurrency(runtime.NumCPU()),
    knowledge.WithDocConcurrency(runtime.NumCPU()*2),
)
```

## Complete Example

The following is a complete example showing how to create an Agent with Knowledge access capabilities:

```go
package main

import (
    "context"
    "flag"
    "log"
    "os"
    "strconv"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"

    // Embedder.
    "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
    geminiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/gemini"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"

    // Source.
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    autosource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/auto"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"

    // Vector Store.
    "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
    vectorpgvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/pgvector"
    vectortcvector "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
)

func main() {
    var (
        embedderType    = flag.String("embedder", "openai", "embedder type (openai, gemini)")
        vectorStoreType = flag.String("vectorstore", "inmemory", "vector store type (inmemory, pgvector, tcvector)")
        modelName       = flag.String("model", "claude-4-sonnet-20250514", "Name of the model to use")
    )

    flag.Parse()

    ctx := context.Background()

    // 1. Create embedder (select based on environment variables).
    var embedder embedder.Embedder
    var err error

    switch *embedderType {
    case "gemini":
        embedder, err = geminiembedder.New(context.Background())
        if err != nil {
            log.Fatalf("Failed to create gemini embedder: %v", err)
        }
    default: // openai.
        embedder = openaiembedder.New(
            openaiembedder.WithModel(getEnvOrDefault("OPENAI_EMBEDDING_MODEL", "text-embedding-3-small")),
        )
    }

    // 2. Create vector store (select based on parameters).
    var vectorStore vectorstore.VectorStore

    switch *vectorStoreType {
    case "pgvector":
        port, err := strconv.Atoi(getEnvOrDefault("PGVECTOR_PORT", "5432"))
        if err != nil {
            log.Fatalf("Failed to convert PGVECTOR_PORT to int: %v", err)
        }

        vectorStore, err = vectorpgvector.New(
            vectorpgvector.WithHost(getEnvOrDefault("PGVECTOR_HOST", "127.0.0.1")),
            vectorpgvector.WithPort(port),
            vectorpgvector.WithUser(getEnvOrDefault("PGVECTOR_USER", "postgres")),
            vectorpgvector.WithPassword(getEnvOrDefault("PGVECTOR_PASSWORD", "")),
            vectorpgvector.WithDatabase(getEnvOrDefault("PGVECTOR_DATABASE", "vectordb")),
            vectorpgvector.WithIndexDimension(1536),
        )
        if err != nil {
            log.Fatalf("Failed to create pgvector store: %v", err)
        }
    case "tcvector":
        vectorStore, err = vectortcvector.New(
            vectortcvector.WithURL(getEnvOrDefault("TCVECTOR_URL", "")),
            vectortcvector.WithUsername(getEnvOrDefault("TCVECTOR_USERNAME", "")),
            vectortcvector.WithPassword(getEnvOrDefault("TCVECTOR_PASSWORD", "")),
        )
        if err != nil {
            log.Fatalf("Failed to create tcvector store: %v", err)
        }
    default: // inmemory.
        vectorStore = vectorinmemory.New()
    }

    // 3. Create knowledge sources.
    sources := []source.Source{
        // File source: Single file processing.
        filesource.New(
            []string{"./data/llm.md"},
            filesource.WithChunkSize(1000),
            filesource.WithChunkOverlap(200),
            filesource.WithName("LLM Documentation"),
            filesource.WithMetadataValue("type", "documentation"),
            filesource.WithMetadataValue("category", "ai"),
        ),

        // Directory source: Batch directory processing.
        dirsource.New(
            []string{"./dir"},
            dirsource.WithRecursive(true),
            dirsource.WithFileExtensions([]string{".md", ".txt"}),
            dirsource.WithChunkSize(800),
            dirsource.WithName("Documentation"),
            dirsource.WithMetadataValue("category", "docs"),
        ),

        // URL source: Get content from web pages.
        urlsource.New(
            []string{"https://en.wikipedia.org/wiki/Artificial_intelligence"},
            urlsource.WithName("Web Documentation"),
            urlsource.WithMetadataValue("source", "web"),
            urlsource.WithMetadataValue("category", "wikipedia"),
            urlsource.WithMetadataValue("language", "en"),
        ),

        // Auto source: Mixed content types.
        autosource.New(
            []string{
                "Cloud computing is the delivery of computing services over the internet, including servers, storage, databases, networking, software, and analytics. It provides on-demand access to shared computing resources.",
                "Machine learning is a subset of artificial intelligence that enables systems to learn and improve from experience without being explicitly programmed.",
                "./README.md",
            },
            autosource.WithName("Mixed Knowledge Sources"),
            autosource.WithMetadataValue("category", "mixed"),
            autosource.WithMetadataValue("type", "custom"),
            autosource.WithMetadataValue("topics", []string{"cloud", "ml", "ai"}),
        ),
    }

    // 4. Create Knowledge.
    kb := knowledge.New(
        knowledge.WithEmbedder(embedder),
        knowledge.WithVectorStore(vectorStore),
        knowledge.WithSources(sources),
    )

    // 5. Load documents (with progress and statistics).
    log.Println("🚀 Starting to load Knowledge ...")
    if err := kb.Load(
        ctx,
        knowledge.WithShowProgress(true),
        knowledge.WithProgressStepSize(10),
        knowledge.WithShowStats(true),
        knowledge.WithSourceConcurrency(4),
        knowledge.WithDocConcurrency(64),
    ); err != nil {
        log.Fatalf("❌ Knowledge loading failed: %v", err)
    }
    log.Println("✅ Knowledge loading completed!")

    // 6. Create LLM model.
    modelInstance := openai.New(*modelName)

    // 7. Create Agent and integrate Knowledge.
    llmAgent := llmagent.New(
        "knowledge-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("Intelligent assistant with Knowledge access capabilities"),
        llmagent.WithInstruction("Use the knowledge_search tool to retrieve relevant information from Knowledge and answer questions based on retrieved content."),
        llmagent.WithKnowledge(kb), // Automatically add knowledge_search tool.
    )

    // 8. Create Runner.
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner(
        "knowledge-chat",
        llmAgent,
        runner.WithSessionService(sessionService),
    )

    // 9. Execute conversation (Agent will automatically use knowledge_search tool).
    log.Println("🔍 Starting to search knowledge base...")
    message := model.NewUserMessage("Please tell me about LLM information")
    eventChan, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }

    // 10. Handle response ...
}

// getEnvOrDefault returns the environment variable value or a default value if not set.
func getEnvOrDefault(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}
```

The environment variable configuration is as follows:

```bash
# OpenAI API configuration (required when using OpenAI embedder, automatically read by OpenAI SDK).
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"
# OpenAI embedding model configuration (optional, needs manual reading in code).
export OPENAI_EMBEDDING_MODEL="text-embedding-3-small"

# Google Gemini API configuration (when using Gemini embedder).
export GOOGLE_API_KEY="your-google-api-key"

# PostgreSQL + pgvector configuration (required when using -vectorstore=pgvector)
export PGVECTOR_HOST="127.0.0.1"
export PGVECTOR_PORT="5432"
export PGVECTOR_USER="postgres"
export PGVECTOR_PASSWORD="your-password"
export PGVECTOR_DATABASE="vectordb"

# TcVector configuration (required when using -vectorstore=tcvector)
export TCVECTOR_URL="https://your-tcvector-endpoint"
export TCVECTOR_USERNAME="your-username"
export TCVECTOR_PASSWORD="your-password"

# Elasticsearch configuration (required when using -vectorstore=elasticsearch)
export ELASTICSEARCH_HOSTS="http://localhost:9200"
export ELASTICSEARCH_USERNAME=""
export ELASTICSEARCH_PASSWORD=""
export ELASTICSEARCH_API_KEY=""
export ELASTICSEARCH_INDEX_NAME="trpc_agent_documents"
```

### Command Line Parameters

```bash
# When running examples, you can select component types through command line parameters.
go run main.go -embedder openai -vectorstore inmemory
go run main.go -embedder gemini -vectorstore pgvector
go run main.go -embedder openai -vectorstore tcvector
go run main.go -embedder openai -vectorstore elasticsearch -es-version v9

# Parameter description:
# -embedder: Select embedder type (openai, gemini), default is openai.
# -vectorstore: Select vector store type (inmemory, pgvector, tcvector, elasticsearch), default is inmemory.
# -es-version: Elasticsearch version (v7, v8, v9), only when vectorstore=elasticsearch.
```

## Troubleshooting

### Common Issues and Handling Suggestions

1. **Create embedding failed/HTTP 4xx/5xx**

   - Possible causes: Invalid or missing API Key; Incorrect BaseURL configuration; Network access restrictions; Text too long; The configured BaseURL doesn't provide Embeddings interface or doesn't support the selected embedding model (e.g., returns 404 Not Found).
   - Troubleshooting steps:
     - Confirm `OPENAI_API_KEY` is set and available;
     - If using compatible gateway, explicitly set `WithBaseURL(os.Getenv("OPENAI_BASE_URL"))`;
     - Confirm `WithModel("text-embedding-3-small")` or the actual embedding model name supported by your service;
     - Use minimal example to call embedding API once to verify connectivity;
     - Use curl to verify if target BaseURL implements `/v1/embeddings` and model exists:
       ```bash
       curl -sS -X POST "$OPENAI_BASE_URL/embeddings" \
         -H "Authorization: Bearer $OPENAI_API_KEY" \
         -H "Content-Type: application/json" \
         -d '{"model":"text-embedding-3-small","input":"ping"}'
       ```
       If returns 404/model doesn't exist, please switch to BaseURL that supports Embeddings or switch to valid embedding model name provided by that service.
     - Gradually shorten text to confirm it's not caused by overly long input.
   - Reference code:
     ```go
     embedder := openaiembedder.New(
         openaiembedder.WithModel("text-embedding-3-small"),
         openaiembedder.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
         openaiembedder.WithBaseURL(os.Getenv("OPENAI_BASE_URL")),
     )
     if _, err := embedder.GetEmbedding(ctx, "ping"); err != nil {
         log.Fatalf("embed check failed: %v", err)
     }
     ```

2. **Slow loading speed or high CPU usage**

   - Possible causes: Single-core sequential loading; Inappropriate concurrency settings; Unreasonable large file chunking strategy.
   - Troubleshooting steps:
     - Set source-level/document-level concurrency: `WithSourceConcurrency(N)`, `WithDocConcurrency(M)`;
     - Adjust chunk size to avoid too many small chunks;
     - Temporarily disable statistics output to reduce log overhead: `WithShowStats(false)`.
   - Reference code:
     ```go
     err := kb.Load(ctx,
         knowledge.WithSourceConcurrency(runtime.NumCPU()),
         knowledge.WithDocConcurrency(runtime.NumCPU()*2),
         knowledge.WithShowStats(false),
     )
     ```

3. **Storage connection failure (pgvector/TcVector)**

   - Possible causes: Incorrect connection parameters; Network/authentication failure; Service not started or port not accessible.
   - Troubleshooting steps:
     - Use native client to connect once first (psql/curl);
     - Explicitly print current configuration (host/port/user/db/url);
     - For minimal example, only insert/query one record to verify.

4. **High memory usage**

   - Possible causes: Loading too many documents at once; Chunk size/overlap too large; Similarity filtering too wide.
   - Troubleshooting steps:
     - Reduce concurrency and chunk overlap;
     - Load directories in batches.

5. **Dimension/vector mismatch**

   - Symptoms: Search phase errors or abnormal scores of 0.
   - Troubleshooting:
     - Confirm embedding model dimension matches existing vectors (`text-embedding-3-small` is 1536);
     - After replacing embedding model, need to rebuild (clear and reload) vector database.

6. **Path/format reading failure**

   - Symptoms: Loading logs show 0 documents or specific source errors.
   - Troubleshooting:
     - Confirm files exist and extensions are supported (.md/.txt/.pdf/.csv/.json/.docx, etc.);
     - Whether directory source needs `WithRecursive(true)`;
     - Use `WithFileExtensions` for whitelist filtering.
