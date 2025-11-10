# Knowledge Usage Documentation

## Overview

Knowledge is the knowledge management system in the tRPC-Agent-Go framework, providing Retrieval-Augmented Generation (RAG) capabilities for Agents. By integrating vector data, embedding models, and document processing components, the Knowledge system enables Agents to access and retrieve relevant knowledge information, thereby providing more accurate and well-founded responses.

### Usage Pattern

The usage of the Knowledge system follows this pattern:

1. **Create Knowledge**: Configure vector storage, Embedder, and knowledge sources
2. **Load Documents**: Load and index documents from various sources
3. **Integrate with Agent**: Use `WithKnowledge()` to integrate Knowledge into LLM Agent
4. **Agent Auto Retrieval**: Agent automatically performs knowledge retrieval through built-in `knowledge_search` tool
5. **Knowledge Base Management**: Enable intelligent synchronization mechanism through `enableSourceSync` to ensure data in vector storage always stays consistent with user-configured sources

This pattern provides:

- **Intelligent Retrieval**: Semantic search based on vector similarity
- **Multi-source Support**: Support for files, directories, URLs, and other knowledge sources
- **Flexible Storage**: Support for memory, PostgreSQL, TcVector, and other storage backends
- **High Performance Processing**: Concurrent processing and batch document loading
- **Knowledge Filtering**: Support static filtering and Agent intelligent filtering through metadata
- **Extensible Architecture**: Support for custom Embedders, Retrievers, and Rerankers
- **Dynamic Management**: Support runtime addition, removal, and updating of knowledge sources
- **Data Consistency Guarantee**: Enable intelligent synchronization mechanism through `enableSourceSync` to ensure vector storage data always stays consistent with user-configured sources, supporting incremental processing, change detection, and automatic orphan document cleanup

### Agent Integration

How the Knowledge system integrates with Agents:

- **Automatic Tool Registration**: Use `WithKnowledge()` option to automatically add `knowledge_search` tool
- **Intelligent Filter Tool**: Use `WithEnableKnowledgeAgenticFilter(true)` to enable `knowledge_search_with_agentic_filter` tool
- **Tool Invocation**: Agents can call knowledge search tools to obtain relevant information
- **Context Enhancement**: Retrieved knowledge content is automatically added to Agent's context
- **Metadata Filtering**: Support precise search based on document metadata

## Quick Start

### Environment Requirements

- Go 1.24.1 or laster
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
        knowledge.WithEnableSourceSync(true), // Enable incremental sync to keep vector storage consistent with sources.
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

### Manual Search Example

```go

package main

import (
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    vectorelasticsearch "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/elasticsearch"
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

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
    // Optional custom method to build documents for retrieval. Falls back to the default if not provided.
    vectorelasticsearch.WithDocBuilder(docBuilder),
)
if err != nil {
    // Handle error.
}

// OpenAI Embedder configuration.
embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"), // Embedding model, can also be set via OPENAI_EMBEDDING_MODEL environment variable.
)

kb := knowledge.New(
    knowledge.WithVectorStore(esVS),
    knowledge.WithEmbedder(embedder),
)

filterCondition := &searchfilter.UniversalFilterCondition{
    Operator: searchfilter.OperatorAnd,
    Value: []*searchfilter.UniversalFilterCondition{
        {
            Field: "tag",
            Operator: searchfilter.OperatorEqual,
            Value: "tag",
        },
        {
            Field: "age",
            Operator: searchfilter.OperatorGreaterThanOrEqual,
            Value: 18,
        },
        {
            Field: "create_time",
            Operator: searchfilter.OperatorBetween,
            Value: []string{"2024-10-11 12:11:00", "2025-10-11 12:11:00"},
        },
        {
            Operator: searchfilter.OperatorOr,
            Value: []*searchfilter.UniversalFilterCondition{
                {
                    Field: "login_time",
                    Operator: searchfilter.OperatorLessThanOrEqual,
                    Value: "2025-01-11 12:11:00",
                },
                {
                    Field: "status",
                    Operator: searchfilter.OperatorEqual,
                    Value: "logout",
                },
            },
        },
    },
}

req := &knowledge.SearchRequest{
    Query: "any text"
    MaxResults: 5,
    MinScore: 0.7,
    SearchFilter: &knowledge.SearchFilter{
        DocumentIDs: []string{"id1","id2"},
        Metadata: map[string]any{
            "title": "title test",
        },
        FilterCondition: filterCondition,
    }
}
searchResult, err := kb.Search(ctx, req)
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
├── reranker/             # reranker layer.
│   ├── reranker.go      # Reranker interface definition.
│   ├── topk.go          # return topk result.
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

The Knowledge system provides two ways to integrate with Agent: automatic integration and manual tool construction.

#### Method 1: Automatic Integration (Recommended)

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

#### Method 2: Manual Tool Construction

Use the manual construction method to configure knowledge base, which allows building multiple knowledge bases.

**Using NewKnowledgeSearchTool to create basic search tool:**

```go
import (
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

// Create Knowledge.
// kb := ...

// Create basic search tool.
searchTool := knowledgetool.NewKnowledgeSearchTool(
    kb,                    // Knowledge instance
    knowledgetool.WithToolName("knowledge_search"),
    knowledgetool.WithToolDescription("Search for relevant information in the knowledge base."),
)

// Create Agent and manually add tool.
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools([]tool.Tool{searchTool}),
)
```

**Using NewAgenticFilterSearchTool to create intelligent filter search tool:**

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

// Get metadata information from sources (for intelligent filtering).
sourcesMetadata := source.GetAllMetadata(sources)

// Create intelligent filter search tool.
filterSearchTool := knowledgetool.NewAgenticFilterSearchTool(
    kb,                    // Knowledge instance
    sourcesMetadata,       // Metadata information
    knowledgetool.WithToolName("knowledge_search_with_filter"),
    knowledgetool.WithToolDescription("Search the knowledge base with intelligent metadata filtering."),
)

llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools([]tool.Tool{filterSearchTool}),
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

docBuilder := func(tcDoc tcvectordb.Document) (*document.Document, []float64, error) {
    return &document.Document{ID: tcDoc.Id}, nil, nil
}

// TcVector.
tcVS, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-tcvector-endpoint"),
    vectortcvector.WithUsername("your-username"),
    vectortcvector.WithPassword("your-password"),
    // Optional custom method to build documents for retrieval. Falls back to the default if not provided.
    vectortcvector.WithDocBuilder(docBuilder),
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
docBuilder := func(hitSource json.RawMessage) (*document.Document, []float64, error) {
    var source struct {
        ID        string    `json:"id"`
        Title     string    `json:"title"`
        Content   string    `json:"content"`
        Page      int       `json:"page"`
        Author    string    `json:"author"`
        CreatedAt time.Time `json:"created_at"`
        UpdatedAt time.Time `json:"updated_at"`
        Embedding []float64 `json:"embedding"`
    }
    if err := json.Unmarshal(hitSource, &source); err != nil {
        return nil, nil, err
    }
    // Create document.
    doc := &document.Document{
        ID:        source.ID,
        Name:      source.Title,
        Content:   source.Content,
        CreatedAt: source.CreatedAt,
        UpdatedAt: source.UpdatedAt,
        Metadata: map[string]any{
            "page":   source.Page,
            "author": source.Author,
        },
    }
    return doc, source.Embedding, nil
}

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
    // Optional custom method to build documents for retrieval. Falls back to the default if not provided.
    vectorelasticsearch.WithDocBuilder(docBuilder),
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

### Reranker

Reranker is responsible for the precise ranking of search results

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
)

rerank := reranker.NewTopKReranker(
    // Specify the number of results to be returned after precision sorting. 
    // If not set, all results will be returned by default
    reranker.WithK(1),
)

kb := knowledge.New(
    knowledge.WithReranker(rerank),
)
```

**Supported embedding models**:

- OpenAI embedding models (text-embedding-3-small, etc.)
- Other OpenAI API compatible embedding services
- Gemini embedding model (via `knowledge/embedder/gemini`)
- Ollama embedding model (via `knowledge/embedder/ollama`)

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

// URL source advanced configuration: Separate content fetching and document identification
urlSrcAlias := urlsource.New(
    []string{"https://trpc-go.com/docs/api.md"},     // Identifier URL (for document ID and metadata)
    urlsource.WithContentFetchingURL([]string{"https://github.com/trpc-group/trpc-go/raw/main/docs/api.md"}), // Actual content fetching URL
    urlsource.WithName("TRPC API Docs"),
    urlsource.WithMetadataValue("source", "github"),
)
// Note: When using WithContentFetchingURL, the identifier URL should preserve the file information from the content fetching URL, for example:
// Correct: Identifier URL is https://trpc-go.com/docs/api.md, fetch URL is https://github.com/.../docs/api.md
// Incorrect: Identifier URL is https://trpc-go.com, which loses document path information

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

## Filter Functionality

The Knowledge system provides powerful filter functionality that allows precise search based on document metadata. This includes both static filters and intelligent filters.

### Basic Filters

Basic filters support two configuration methods: Agent-level fixed filters and Runner-level runtime filters.

#### Agent-level Filters

Preset fixed search filter conditions when creating an Agent:

```go
// Create Agent with fixed filters
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb),
    llmagent.WithKnowledgeFilter(map[string]interface{}{
        "category": "documentation",
        "topic":    "programming",
    }),
)
```

#### Runner-level Filters

Dynamically pass filters when calling `runner.Run()`, suitable for scenarios that require filtering based on different request contexts:

```go
import "trpc.group/trpc-go/trpc-agent-go/agent"

// Pass filters at runtime
eventCh, err := runner.Run(
    ctx,
    userID,
    sessionID,
    message,
    agent.WithKnowledgeFilter(map[string]interface{}{
        "user_level": "premium",     // Filter by user level
        "region":     "china",       // Filter by region
        "language":   "zh",          // Filter by language
    }),
)
```

**Important**: Agent-level filters have higher priority than Runner-level filters, and values with the same key will be overridden by Agent-level:

```go
// Agent-level filter
llmAgent := llmagent.New(
    "assistant",
    llmagent.WithKnowledge(kb),
    llmagent.WithKnowledgeFilter(map[string]interface{}{
        "category": "general",
        "source":   "internal",
    }),
)

// Runner-level filter's same keys will be overridden by Agent-level
eventCh, err := runner.Run(
    ctx, userID, sessionID, message,
    agent.WithKnowledgeFilter(map[string]interface{}{
        "source": "external",  // Will be overridden by Agent-level "internal"
        "topic":  "api",       // Add new filter condition (not in Agent-level)
    }),
)

// Final effective filter:
// {
//     "category": "general",   // From Agent-level
//     "source":   "internal",  // From Agent-level (overrode Runner-level "external")
//     "topic":    "api",       // From Runner-level (added)
// }
```

### Intelligent Filters (Agentic Filter)

Intelligent filters are an advanced feature of the Knowledge system that allows LLM Agents to dynamically select appropriate filter conditions based on user queries.

#### Enable Intelligent Filters

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

// Get metadata information from all sources
sourcesMetadata := source.GetAllMetadata(sources)

// Create Agent with intelligent filter support
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb),
    llmagent.WithEnableKnowledgeAgenticFilter(true),           // Enable intelligent filters
    llmagent.WithKnowledgeAgenticFilterInfo(sourcesMetadata), // Provide available filter information
)
```

#### Filter Layers

The Knowledge system supports multi-layer filters, all implemented using FilterCondition and combined with **AND logic**. The system does not distinguish priority; all layer filters are merged equally.

**Filter Layers**:

1. **Agent-level Filters**:
   - Metadata filters set via `llmagent.WithKnowledgeFilter()`
   - Complex condition filters set via `llmagent.WithKnowledgeConditionedFilter()`

2. **Tool-level Filters**:
   - Metadata filters set via `tool.WithFilter()`
   - Complex condition filters set via `tool.WithConditionedFilter()`
   - Note: Agent-level filters are actually implemented through Tool-level filters

3. **Runner-level Filters**:
   - Metadata filters passed via `agent.WithKnowledgeFilter()` in `runner.Run()`
   - Complex condition filters passed via `agent.WithKnowledgeConditionedFilter()` in `runner.Run()`

4. **LLM Intelligent Filters**:
   - Filters dynamically generated by LLM based on user queries (complex condition filters only)

> **Important Notes**:
> - All filters are combined with **AND logic**, meaning documents must satisfy all filter conditions at all levels
> - There is no priority override relationship; all filters are equal constraints
> - Each level supports both metadata filters and complex condition filters (except LLM, which only supports complex conditions)

##### Example: Filter Combination

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"

// 1. Agent-level filters
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb),
    // Agent-level metadata filter
    llmagent.WithKnowledgeFilter(map[string]interface{}{
        "source":   "official",      // Official source
        "category": "documentation", // Documentation category
    }),
    // Agent-level complex condition filter
    llmagent.WithKnowledgeConditionedFilter(
        searchfilter.Equal("status", "published"), // Published status
    ),
)

// 2. Runner-level filters
eventCh, err := runner.Run(
    ctx, userID, sessionID, message,
    // Runner-level metadata filter
    agent.WithKnowledgeFilter(map[string]interface{}{
        "region":   "china",  // China region
        "language": "zh",     // Chinese language
    }),
    // Runner-level complex condition filter
    agent.WithKnowledgeConditionedFilter(
        searchfilter.GreaterThan("priority", 5), // Priority greater than 5
    ),
)

// 3. LLM intelligent filter (dynamically generated by LLM)
// Example: User asks "find API related docs", LLM might generate {"topic": "api"}

// Final effective filter conditions (all combined with AND):
// source = "official" AND 
// category = "documentation" AND 
// status = "published" AND
// region = "china" AND 
// language = "zh" AND 
// priority > 5 AND
// topic = "api"
//
// i.e., must satisfy all conditions at all levels
```

##### Complex Condition Filter Example

```go
// Manually create Tool with complex condition filters
searchTool := tool.NewKnowledgeSearchTool(
    kb,
    // Agent-level metadata filter
    tool.WithFilter(map[string]interface{}{
        "source": "official",
    }),
    // Agent-level complex condition filter
    tool.WithConditionedFilter(
        searchfilter.Or(
            searchfilter.Equal("topic", "programming"),
            searchfilter.Equal("topic", "llm"),
        ),
    ),
)

llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools(searchTool),  // Manually pass Tool
)

// Final filter conditions:
// source = "official" AND (topic = "programming" OR topic = "llm")
// i.e., must be official source AND topic is either programming or LLM
```

##### Common Filter Helper Functions

```go
// Comparison operators
searchfilter.Equal(field, value)              // field = value
searchfilter.NotEqual(field, value)           // field != value
searchfilter.GreaterThan(field, value)        // field > value
searchfilter.GreaterThanOrEqual(field, value) // field >= value
searchfilter.LessThan(field, value)           // field < value
searchfilter.LessThanOrEqual(field, value)    // field <= value
searchfilter.In(field, values...)             // field IN (...)
searchfilter.NotIn(field, values...)          // field NOT IN (...)
searchfilter.Like(field, pattern)             // field LIKE pattern
searchfilter.Between(field, min, max)         // field BETWEEN min AND max

// Logical operators
searchfilter.And(conditions...)               // AND combination
searchfilter.Or(conditions...)                // OR combination

// Nested example: (status = 'published') AND (category = 'doc' OR category = 'tutorial')
searchfilter.And(
    searchfilter.Equal("status", "published"),
    searchfilter.Or(
        searchfilter.Equal("category", "documentation"),
        searchfilter.Equal("category", "tutorial"),
    ),
)
```

### Metadata Configuration

To make intelligent filters work properly, you need to add rich metadata when creating document sources:

#### Metadata Acquisition

The Knowledge system provides utility functions to collect metadata information from sources:

```go
import "trpc.group/trpc-go/trpc-agent-go/knowledge/source"

// Get all metadata key-value pairs from all sources
// Returns map[string][]any containing all possible metadata values
sourcesMetadata := source.GetAllMetadata(sources)

// Get all metadata keys from all sources
// Returns []string containing all metadata field names
metadataKeys := source.GetAllMetadataKeys(sources)
```

#### Source Metadata Configuration

```go
sources := []source.Source{
    // File source metadata configuration
    filesource.New(
        []string{"./docs/api.md"},
        filesource.WithName("API Documentation"),
        filesource.WithMetadataValue("category", "documentation"),
        filesource.WithMetadataValue("topic", "api"),
        filesource.WithMetadataValue("service_type", "gateway"),
        filesource.WithMetadataValue("protocol", "trpc-go"),
        filesource.WithMetadataValue("version", "v1.0"),
    ),

    // Directory source metadata configuration
    dirsource.New(
        []string{"./tutorials"},
        dirsource.WithName("Tutorials"),
        dirsource.WithMetadataValue("category", "tutorial"),
        dirsource.WithMetadataValue("difficulty", "beginner"),
        dirsource.WithMetadataValue("topic", "programming"),
    ),

    // URL source metadata configuration
    urlsource.New(
        []string{"https://example.com/wiki/rpc"},
        urlsource.WithName("RPC Wiki"),
        urlsource.WithMetadataValue("category", "encyclopedia"),
        urlsource.WithMetadataValue("source_type", "web"),
        urlsource.WithMetadataValue("topic", "rpc"),
        urlsource.WithMetadataValue("language", "zh"),
    ),
}
```

### Vector Database Filter Support

Different vector databases have varying levels of filter support:

#### PostgreSQL + pgvector

- ✅ Supports all metadata field filtering
- ✅ Supports complex query conditions
- ✅ Supports JSONB field indexing

```go
vectorStore, err := vectorpgvector.New(
    vectorpgvector.WithHost("127.0.0.1"),
    vectorpgvector.WithPort(5432),
    // ... other configurations
)
```

#### TcVector

- ✅ Supports all metadata filtering
- ✅ v0.4.0+ new collections automatically support JSON index (requires TCVector service support)
- ⚡ Optional: Use `WithFilterIndexFields` to build additional indexes for frequently queried fields

```go
// v0.4.0+ new collections (TCVector service supports JSON index)
vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    // ... other configurations
)
// All metadata fields are queryable via JSON index, no need to predefine

// Optional: Build additional indexes for high-frequency fields to optimize performance
metadataKeys := source.GetAllMetadataKeys(sources)
vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    vectortcvector.WithFilterIndexFields(metadataKeys), // Optional: build additional indexes
    // ... other configurations
)

// Collections before v0.4.0 or TCVector service without JSON index support
vectorStore, err := vectortcvector.New(
    vectortcvector.WithURL("https://your-endpoint"),
    vectortcvector.WithFilterIndexFields(metadataKeys), // Required: predefine filter fields
    // ... other configurations
)
```

**Notes:**
- **v0.4.0+ new collections**: Automatically create metadata JSON index, all fields queryable
- **Legacy collections**: Only fields in `WithFilterIndexFields` are queryable

#### In-memory Storage

- ✅ Supports all filter functionality
- ⚠️ Only suitable for development and testing

### Knowledge Base Management Functionality

The Knowledge system provides powerful knowledge base management functionality, supporting dynamic source management and intelligent synchronization mechanisms.

#### Enable Source Sync (enableSourceSync)

By enabling `enableSourceSync`, the knowledge base will always keep vector storage data consistent with configured sources. If you're not using custom methods to manage the knowledge base, it's recommended to enable this option:

```go
kb := knowledge.New(
    knowledge.WithEmbedder(embedder),
    knowledge.WithVectorStore(vectorStore),
    knowledge.WithSources(sources),
    knowledge.WithEnableSourceSync(true), // Enable incremental sync
)
```

**How the synchronization mechanism works**:

1. **Pre-loading preparation**: Refresh document information cache, establish synchronization state tracking
2. **Process tracking**: Record processed documents to avoid duplicate processing
3. **Post-loading cleanup**: Automatically clean up orphaned documents that no longer exist

**Advantages of enabling synchronization**:

- **Data consistency**: Ensure vector storage is completely synchronized with source configuration
- **Incremental updates**: Only process changed documents, improving performance
- **Orphan cleanup**: Automatically delete documents from removed sources
- **State tracking**: Real-time monitoring of synchronization status and processing progress

#### Dynamic Source Management

Knowledge supports runtime dynamic management of knowledge sources, ensuring data in vector storage always stays consistent with user-configured sources:

```go
// Add new knowledge source - data will stay consistent with configured sources
newSource := filesource.New([]string{"./new-docs/api.md"})
if err := kb.AddSource(ctx, newSource); err != nil {
    log.Printf("Failed to add source: %v", err)
}

// Reload specified knowledge source - automatically detect changes and sync
if err := kb.ReloadSource(ctx, newSource); err != nil {
    log.Printf("Failed to reload source: %v", err)
}

// Remove specified knowledge source - precisely delete related documents
if err := kb.RemoveSource(ctx, "API Documentation"); err != nil {
    log.Printf("Failed to remove source: %v", err)
}
```

**Core features of dynamic management**:

- **Data consistency guarantee**: Vector storage data always stays consistent with user-configured sources
- **Intelligent incremental sync**: Only process changed documents, avoiding duplicate processing
- **Precise source control**: Support precise addition/removal/reload by source name
- **Orphan document cleanup**: Automatically clean up documents that no longer belong to any configured source
- **Hot update support**: Update knowledge base without restarting the application

#### Knowledge Base Status Monitoring

Knowledge provides rich status monitoring functionality to help users understand the synchronization status of currently configured sources:

```go
// Show all document information
docInfos, err := kb.ShowDocumentInfo(ctx)
if err != nil {
    log.Printf("Failed to show document info: %v", err)
    return
}

// Also supports querying specific sources or metadata
// docInfos, err := kb.ShowDocumentInfo(ctx, "source_name_1", "source_name_2")
// This will only return document metadata for the specified source names

// Iterate through document information
for _, docInfo := range docInfos {
    fmt.Printf("Document ID: %s\n", docInfo.DocumentID)
    fmt.Printf("Source: %s\n", docInfo.SourceName)
    fmt.Printf("URI: %s\n", docInfo.URI)
    fmt.Printf("Chunk Index: %d\n", docInfo.ChunkIndex)
}
```

**Status monitoring output example**:

```
Document ID: a1b2c3d4e5f6...
Source: Technical Documentation
URI: /docs/api/authentication.md
Chunk Index: 0

Document ID: f6e5d4c3b2a1...
Source: Technical Documentation
URI: /docs/api/authentication.md
Chunk Index: 1
```

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
	ollamaembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/ollama"

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
        embedderType    = flag.String("embedder", "openai", "embedder type (openai, gemini, ollama)")
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
	case "ollama":
		embedder, err = ollamaembedder.New()
		if err != nil {
			log.Fatalf("Failed to create ollama embedder: %v", err)
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

    // Get metadata information from all sources (for intelligent filters).
    sourcesMetadata := source.GetAllMetadata(sources)

    // 7. Create Agent and integrate Knowledge.
    llmAgent := llmagent.New(
        "knowledge-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithDescription("Intelligent assistant with Knowledge access capabilities"),
        llmagent.WithInstruction("Use the knowledge_search or knowledge_search_with_filter tool to retrieve relevant information from Knowledge and answer questions based on retrieved content. Select appropriate filter conditions based on user queries."),
        llmagent.WithKnowledge(kb), // Automatically add knowledge_search tool.
        llmagent.WithEnableKnowledgeAgenticFilter(true),           // Enable intelligent filters
        llmagent.WithKnowledgeAgenticFilterInfo(sourcesMetadata), // Provide available filter information
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

    // 11. Demonstrate knowledge base management functionality - view document metadata
    log.Println("📊 Displaying current knowledge base status...")

    // Query metadata information for all documents, also supports querying specific source or metadata data
    docInfos, err := kb.ShowDocumentInfo(ctx)
    if err != nil {
        log.Printf("Failed to show document info: %v", err)
    } else {
        log.Printf("Knowledge base contains a total of %d document chunks", len(docInfos))
    }


    // 12. Demonstrate dynamic source addition - new data will automatically stay consistent with configuration
    log.Println("Demonstrating dynamic source addition...")
    newSource := filesource.New(
        []string{"./new-docs/changelog.md"},
        filesource.WithName("Changelog"),
        filesource.WithMetadataValue("category", "changelog"),
        filesource.WithMetadataValue("type", "updates"),
    )

    if err := kb.AddSource(ctx, newSource); err != nil {
        log.Printf("Failed to add new source: %v", err)
    }

    // 13. Demonstrate source removal (optional, uncomment to test)
    // if err := kb.RemoveSource(ctx, "Changelog"); err != nil {
    //     log.Printf("Failed to remove source: %v", err)
    // }
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
# -embedder: Select embedder type (openai, gemini, ollama), default is openai.
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
