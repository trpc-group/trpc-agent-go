# Knowledge Documentation

## Overview

Knowledge is the knowledge management system in the tRPC-Agent-Go framework, providing Retrieval-Augmented Generation (RAG) capabilities for Agents. By integrating vector data, embedding models, and document processing components, the Knowledge system helps Agents access and retrieve relevant knowledge information, providing more accurate and well-grounded responses.

### Usage Pattern

The Knowledge system follows this usage pattern:

1. **Create Knowledge**: Configure vector store, Embedder, and knowledge sources
2. **Load Documents**: Load and index documents from various sources
3. **Create Search Tool**: Use `NewKnowledgeSearchTool` to create a knowledge search tool
4. **Integrate with Agent**: Add the search tool to the Agent's tool list
5. **Knowledge Base Management**: Enable intelligent sync mechanism via `enableSourceSync` to keep vector store data consistent with configured sources

This pattern provides:

- **Intelligent Retrieval**: Semantic search based on vector similarity
- **Multi-source Support**: Support for files, directories, URLs, and other knowledge sources
- **Flexible Storage**: Support for in-memory, PostgreSQL, TcVector, and other storage backends
- **High-performance Processing**: Concurrent processing and batch document loading
- **Knowledge Filtering**: Support for static filtering and Agent intelligent filtering via metadata
- **Extensible Architecture**: Support for custom Embedder, Retriever, and Reranker

### Agent Integration

Ways to integrate the Knowledge system with Agents:

- **Manual Tool Creation (Recommended)**: Use `NewKnowledgeSearchTool` to create search tools with flexible tool names and descriptions, supporting multiple knowledge bases
- **Intelligent Filter Tool**: Use `NewAgenticFilterSearchTool` to create search tools with intelligent filtering
- **Auto Integration**: Use `WithKnowledge()` option to automatically add `knowledge_search` tool (for simple scenarios)
- **Context Enhancement**: Retrieved knowledge content is automatically added to the Agent's context
- **Metadata Filtering**: Support for precise search based on document metadata

## Quick Start

> **Complete Example**: [examples/knowledge/basic](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge/basic)

### Requirements

- Valid LLM API key (OpenAI compatible interface)
- Vector database (optional, for production environments)

### Environment Variables

```bash
# OpenAI API configuration
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"

# Embedding model configuration (optional, requires manual reading)
export OPENAI_EMBEDDING_MODEL="text-embedding-3-small"
```

### Minimal Example

```go
package main

import (
    "context"
    "log"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/knowledge"
    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
    filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
    vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/tool"

    // To support PDF files, manually import PDF reader (separate go.mod to avoid unnecessary dependencies)
    // _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)

func main() {
    ctx := context.Background()

    // 1. Create embedder
    embedder := openaiembedder.New(
        openaiembedder.WithModel("text-embedding-3-small"),
    )

    // 2. Create vector store
    vectorStore := vectorinmemory.New()

    // 3. Create knowledge sources, auto-detects file formats
    sources := []source.Source{
        filesource.New([]string{"./data/llm.md"}),
        dirsource.New([]string{"./dir"}),
    }

    // 4. Create Knowledge
    kb := knowledge.New(
        knowledge.WithEmbedder(embedder),
        knowledge.WithVectorStore(vectorStore),
        knowledge.WithSources(sources),
        knowledge.WithEnableSourceSync(true),
    )

    // 5. Load documents
    if err := kb.Load(ctx); err != nil {
        log.Fatalf("Failed to load knowledge base: %v", err)
    }

    // 6. Create search tool
    searchTool := knowledgetool.NewKnowledgeSearchTool(
        kb,
        knowledgetool.WithToolName("knowledge_search"),
        knowledgetool.WithToolDescription("Search for relevant information in the knowledge base."),
    )

    // 7. Create Agent and add tools
    modelInstance := openai.New("claude-4-sonnet-20250514")
    llmAgent := llmagent.New(
        "knowledge-assistant",
        llmagent.WithModel(modelInstance),
        llmagent.WithTools([]tool.Tool{searchTool}),
    )

    // 8. Create Runner and execute
    sessionService := inmemory.NewSessionService()
    appRunner := runner.NewRunner("knowledge-chat", llmAgent, runner.WithSessionService(sessionService))

    message := model.NewUserMessage("Tell me about LLM")
    _, err := appRunner.Run(ctx, "user123", "session456", message)
    if err != nil {
        log.Fatalf("Failed to run agent: %v", err)
    }
}
```


## Core Concepts

The [knowledge module](https://github.com/trpc-group/trpc-agent-go/tree/main/knowledge) is the knowledge management core of the tRPC-Agent-Go framework, providing complete RAG capabilities. The module uses a modular design, supporting various document sources, vector storage backends, and embedding models.

```
knowledge/
├── knowledge.go          # Core interface definitions and main implementation
├── source/               # Document source management
│   ├── source.go        # Source interface definition
│   ├── file/            # File source implementation
│   ├── dir/             # Directory source implementation
│   ├── url/             # URL source implementation
│   └── auto/            # Auto source type detection
├── vectorstore/          # Vector storage backends
│   ├── vectorstore.go   # VectorStore interface definition
│   ├── inmemory/        # In-memory vector store (dev/test)
│   ├── pgvector/        # PostgreSQL + pgvector implementation
│   ├── tcvector/        # Tencent Cloud vector database implementation
│   ├── elasticsearch/   # Elasticsearch implementation
│   ├── milvus/          # Milvus vector database implementation
│   └── qdrant/          # Qdrant vector database implementation
├── embedder/             # Text embedding models
│   ├── embedder.go      # Embedder interface definition
│   ├── openai/          # OpenAI embedding model
│   ├── gemini/          # Gemini embedding model
│   ├── ollama/          # Ollama local embedding model
│   └── huggingface/     # HuggingFace embedding model
├── reranker/             # Result reranking
│   ├── reranker.go      # Reranker interface definition
│   ├── topk/            # TopK simple truncation implementation
│   ├── cohere/          # Cohere SaaS Rerank implementation
│   └── infinity/        # Infinity/TEI standard Rerank API implementation
├── document/             # Document processing
│   ├── document.go      # Document structure definition
│   └── reader/          # Document readers (supports txt/md/csv/json/docx/pdf, etc.)
├── query/                # Query enhancers
│   ├── query.go         # QueryEnhancer interface definition
│   └── passthrough.go   # Default passthrough enhancer
└── ocr/                  # OCR text recognition
    ├── ocr.go           # Extractor interface definition
    └── tesseract/       # Tesseract OCR implementation (separate go.mod)
```

## Agent Integration

The Knowledge system provides search tools to integrate knowledge base capabilities into Agents.

### Search Tools

#### KnowledgeSearchTool

Basic search tool supporting semantic search and static filtering:

```go
import (
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

searchTool := knowledgetool.NewKnowledgeSearchTool(
    kb,
    knowledgetool.WithToolName("knowledge_search"),
    knowledgetool.WithToolDescription("Search for relevant information in the knowledge base."),
    knowledgetool.WithMaxResults(10),
    knowledgetool.WithMinScore(0.5),
)
```

#### AgenticFilterSearchTool

Intelligent filter search tool where the Agent can automatically build filter conditions based on user queries:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/knowledge/source"
    knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
)

// Get source metadata information (for intelligent filtering)
sourcesMetadata := source.GetAllMetadata(sources)

filterSearchTool := knowledgetool.NewAgenticFilterSearchTool(
    kb,                    // Knowledge instance
    sourcesMetadata,       // Metadata information
    knowledgetool.WithToolName("knowledge_search_with_filter"),
    knowledgetool.WithToolDescription("Search the knowledge base with intelligent metadata filtering."),
)
```

#### Search Tool Configuration Options

Both `NewKnowledgeSearchTool` and `NewAgenticFilterSearchTool` support the following configuration options:

| Option | Description | Default |
|--------|-------------|---------|
| `WithToolName(name)` | Set tool name | `"knowledge_search"` / `"knowledge_search_with_agentic_filter"` |
| `WithToolDescription(desc)` | Set tool description | Default description |
| `WithMaxResults(n)` | Set maximum number of documents to return | `10` |
| `WithMinScore(score)` | Set minimum relevance score threshold (0.0-1.0), documents below this score will be filtered | `0.0` |
| `WithFilter(map)` | Set static metadata filter (simple AND logic) | `nil` |
| `WithConditionedFilter(cond)` | Set complex filter conditions (supports AND/OR/nested logic) | `nil` |

### Integration Methods

#### Method 1: Manual Tool Addition (Recommended)

Use `llmagent.WithTools` to manually add search tools with flexible configuration and support for multiple knowledge bases:

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools([]tool.Tool{searchTool, filterSearchTool}),
)
```

#### Method 2: Automatic Integration

Use `llmagent.WithKnowledge(kb)` to integrate Knowledge into the Agent, and the framework will automatically register the `knowledge_search` tool.

> **Note**: The automatic integration method is simple and quick, but less flexible. It doesn't allow customizing tool names, descriptions, filter conditions, or other parameters, and doesn't support integrating multiple knowledge bases simultaneously. For more fine-grained control, it's recommended to use the manual tool addition approach.

```go
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb), // Automatically adds knowledge_search tool
)
```

## More Content

- [Vector Store](vectorstore/index.md) - Configure various vector database backends
- [Embedder](embedder.md) - Text vectorization model configuration
- [Document Sources](source.md) - File, directory, URL, and other knowledge source configuration
- [OCR Text Recognition](ocr.md) - Configure Tesseract OCR for text extraction
- [Filters](filter.md) - Basic filters and intelligent filters
- [Knowledge Base Management](management.md) - Dynamic source management and status monitoring
