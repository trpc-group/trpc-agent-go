# Knowledge Integration Example

This example demonstrates how to integrate a knowledge base with the LLM agent in `trpc-agent-go`.

## Features

- **Multiple Vector Store Support**: Choose between in-memory, pgvector (PostgreSQL), or tcvector storage backends
- **OpenAI Embedder Integration**: Uses OpenAI embeddings for high-quality document representation
- **Rich Knowledge Sources**: Supports file, directory, URL, and auto-detection sources
- **Interactive Chat Interface**: Features knowledge search, calculator, and current time tools
- **Streaming Response**: Real-time streaming of LLM responses with tool execution feedback

## Knowledge Sources Loaded

The following sources are automatically loaded when you run `main.go`:

| Source Type | Name / File | What It Covers |
|-------------|-------------|----------------|
| File        | `./data/llm.md` | Large-Language-Model (LLM) basics. |
| Directory   | `./dir/transformer.pdf` | Concise primer on the Transformer architecture and self-attention. |
| Directory   | `./dir/moe.txt` | Notes about Mixture-of-Experts (MoE) models. |
| URL         | <https://en.wikipedia.org/wiki/Byte-pair_encoding> | Byte-pair encoding (BPE) algorithm. |
| Auto Source | Mixed content (Cloud computing blurb, N-gram Wikipedia page, project README) | Cloud computing overview and N-gram language models. |

These documents are embedded and indexed, enabling the `knowledge_search` tool to answer related questions.

### Try Asking Questions Like

```
• What problem does self-attention solve in Transformers?
• Explain the benefits of Mixture-of-Experts models.
• How is Byte-pair encoding used in tokenization?
• Give an example of an N-gram model in NLP.
• What are common use-cases for cloud computing?
```

## Usage

### Prerequisites

1. **Set OpenAI API Key** (Required)
   ```bash
   export OPENAI_API_KEY="your-openai-api-key"
   ```

2. **Configure Vector Store** (Optional - defaults to in-memory)
   
   For persistent storage, configure the appropriate environment variables for your chosen vector store.

### Running the Example

```bash
cd examples/knowledge

# Use in-memory vector store (default)
go run main.go

# Use PostgreSQL with pgvector
go run main.go -vectorstore=pgvector

# Use TcVector
go run main.go -vectorstore=tcvector

# Specify a different model
go run main.go -model="gpt-4o-mini" -vectorstore=pgvector
```

### Interactive Commands

- **Regular chat**: Type your questions naturally
- **`/history`**: Show conversation history
- **`/new`**: Start a new session
- **`/exit`**: End the conversation

## Available Tools

| Tool | Description | Example Usage |
|------|-------------|---------------|
| `knowledge_search` | Search the knowledge base for relevant information | "What is a Large Language Model?" |
| `calculator` | Perform mathematical calculations (add, subtract, multiply, divide, power) | "Calculate 15 * 23" |
| `current_time` | Get current time and date for specific timezones | "What time is it in PST?" |

## Vector Store Options

### In-Memory (Default)
- **Pros**: No external dependencies, fast for small datasets
- **Cons**: Data doesn't persist between runs
- **Use case**: Development, testing, small knowledge bases

### PostgreSQL with pgvector 
- **Use case**: Production deployments, persistent storage
- **Setup**: Requires PostgreSQL with pgvector extension

### TcVector
- **Use case**: Cloud deployments, managed vector storage
- **Setup**: Requires TcVector service credentials

## Configuration

### Required Environment Variables

- `OPENAI_API_KEY`: Your OpenAI API key for embeddings and chat

### Optional Configuration

- `OPENAI_EMBEDDING_MODEL`: OpenAI embedding model (default: `text-embedding-3-small`)
- Vector store specific variables (see vector store documentation for details)

### Command Line Options

```bash
-model string     LLM model name (default: "claude-4-sonnet-20250514")
-vectorstore string   Vector store type: inmemory, pgvector, tcvector (default: "inmemory")
```

---

For more details, see the code in `main.go`.

## How It Works

### 1. Knowledge Base Setup

The example creates a knowledge base with configurable vector store:

```go
// Create knowledge base with configurable vector store
vectorStore, err := c.setupVectorDB() // Supports inmemory, pgvector, tcvector
embedder := openaiembedder.New()

kb := knowledge.New(
    knowledge.WithVectorStore(vectorStore),
    knowledge.WithEmbedder(embedder),
    knowledge.WithSources(sources),
)
```

### 2. Knowledge Sources

The example demonstrates multiple source types:

```go
sources := []source.Source{
    // File source for local documentation
    filesource.New(
        []string{"./data/llm.md"},
        filesource.WithName("Large Language Model"),
        filesource.WithMetadataValue("type", "documentation"),
    ),
    
    // Directory source for multiple files
    dirsource.New(
        []string{"./dir"},
        dirsource.WithName("Data Directory"),
    ),
    
    // URL source for web content
    urlsource.New(
        []string{"https://en.wikipedia.org/wiki/Byte-pair_encoding"},
        urlsource.WithName("Byte-pair encoding"),
        urlsource.WithMetadataValue("source", "wikipedia"),
    ),
    
    // Auto source handles mixed content types
    autosource.New(
        []string{
            "Cloud computing is the delivery...", // Direct text
            "https://en.wikipedia.org/wiki/N-gram", // URL
            "./README.md", // File
        },
        autosource.WithName("Mixed Content Source"),
    ),
}
```

### 3. LLM Agent Configuration

The agent is configured with the knowledge base using the `WithKnowledge()` option:

```go
llmAgent := llmagent.New(
    "knowledge-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithKnowledge(kb), // This automatically adds the knowledge_search tool
    llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
    // ... other options
)
```

### 4. Automatic Tool Registration

When `WithKnowledge()` is used, the agent automatically gets access to the `knowledge_search` tool, which allows it to:

- Search the knowledge base for relevant information
- Retrieve document content based on queries
- Use the retrieved information to answer user questions

## Implementation Details

### Knowledge Interface

The knowledge integration uses the `knowledge.Knowledge` interface:

```go
type Knowledge interface {
    Search(ctx context.Context, query string) (*SearchResult, error)
}
```

### BuiltinKnowledge Implementation

The example uses `BuiltinKnowledge` which provides:

- **Storage**: In-memory document storage
- **Vector Store**: In-memory vector similarity search
- **Embedder**: Mock embedder for demonstration
- **Retriever**: Complete RAG pipeline with query enhancement and reranking

### Knowledge Search Tool

The `knowledge_search` tool is automatically created by `knowledgetool.NewKnowledgeSearchTool()` and provides:

- Query validation
- Search execution
- Result formatting with relevance scores
- Error handling

### Components Used

- **Vector Stores**:
  - `vectorstore/inmemory`: In-memory vector store with cosine similarity
  - `vectorstore/pgvector`: PostgreSQL-based persistent vector storage
  - `vectorstore/tcvector`: TcVector cloud-native vector storage
- **Embedder**: `embedder/openai`: OpenAI embeddings API integration
- **Sources**: `source/{file,dir,url,auto}`: Multiple content source types
- **Session**: `session/inmemory`: In-memory conversation state management

## Extending the Example

### Adding Custom Sources

```go
// Add your own content sources
customSources := []source.Source{
    filesource.New(
        []string{"/path/to/your/docs/*.md"},
        filesource.WithName("Custom Documentation"),
    ),
    urlsource.New(
        []string{"https://your-company.com/api-docs"},
        urlsource.WithMetadataValue("category", "api"),
    ),
}

// Append to existing sources
allSources := append(sources, customSources...)
```

### Production Considerations

- Use persistent vector store (`pgvector` or `tcvector`) for production
- Secure API key management
- Monitor vector store performance
- Implement proper error handling and logging

## Example Files

| File | Description |
|------|-------------|
| `main.go` | Complete knowledge integration example with multi-vector store support |
| `data/llm.md` | Sample documentation about Large Language Models |
| `dir/transformer.pdf` | Transformer architecture documentation |
| `dir/moe.txt` | Mixture-of-Experts model notes |
| `README.md` | This comprehensive documentation |

## Key Dependencies

- `agent/llmagent`: LLM agent with streaming and tool support
- `knowledge/*`: Complete RAG pipeline with multiple source types
- `knowledge/vectorstore/*`: Multiple vector storage backends
- `knowledge/embedder/openai`: OpenAI embeddings integration
- `runner`: Multi-turn conversation management with session state
- `tool/function`: Custom tool creation utilities

## Troubleshooting

### Common Issues

1. **OpenAI API Key Error**
   - Ensure `OPENAI_API_KEY` is set correctly
   - Verify your OpenAI account has embedding API access

2. **Vector Store Connection Issues**
   - For pgvector: Ensure PostgreSQL is running and pgvector extension is installed
   - For tcvector: Verify service credentials and network connectivity
   - Check environment variables are set correctly

4. **Knowledge Loading Errors**
   - Verify source files/URLs are accessible
   - Check file permissions for local sources
   - Ensure stable internet connection for URL sources 