# Knowledge Integration Example

This example demonstrates how to integrate a knowledge base with the LLM agent in `trpc-agent-go`.

## Features

- **Multiple Vector Store Support**: Choose between in-memory, pgvector (PostgreSQL), or tcvector storage backends
- **Multiple Embedder Support**: OpenAI and Gemini embedder options
- **Rich Knowledge Sources**: Supports file, directory, URL, and auto-detection sources
- **Interactive Chat Interface**: Features knowledge search with multi-turn conversation support
- **Streaming Response**: Real-time streaming of LLM responses with tool execution feedback
- **Session Management**: Maintains conversation history and supports new session creation

## Knowledge Sources Loaded

The following sources are automatically loaded when you run `main.go`:

| Source Type | Name / File                                                                  | What It Covers                                       |
| ----------- | ---------------------------------------------------------------------------- | ---------------------------------------------------- |
| File        | `./data/llm.md`                                                              | Large-Language-Model (LLM) basics.                   |
| Directory   | `./dir/`                                                                     | Various documents in the directory.                  |
| URL         | <https://en.wikipedia.org/wiki/Byte-pair_encoding>                           | Byte-pair encoding (BPE) algorithm.                  |
| Auto Source | Mixed content (Cloud computing blurb, N-gram Wikipedia page, project README) | Cloud computing overview and N-gram language models. |

These documents are embedded and indexed, enabling the `knowledge_search` tool to answer related questions.

### Try Asking Questions Like

```
• What is a Large Language Model?
• Explain the Transformer architecture.
• What is a Mixture-of-Experts (MoE) model?
• How does Byte-pair encoding work?
• What is an N-gram model?
• What is cloud computing?
```

## Usage

### Prerequisites

1. **Set OpenAI API Key** (Required for OpenAI model and embedder)

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

# Use Gemini embedder
go run main.go -embedder=gemini

# Disable streaming mode
go run main.go -streaming=false
```

### Interactive Commands

- **Regular chat**: Type your questions naturally
- **`/history`**: Show conversation history
- **`/new`**: Start a new session
- **`/exit`**: End the conversation

## Available Tools

| Tool               | Description                                        | Example Usage                     |
| ------------------ | -------------------------------------------------- | --------------------------------- |
| `knowledge_search` | Search the knowledge base for relevant information | "What is a Large Language Model?" |

## Vector Store Options

### In-Memory (Default)

- **Pros**: No external dependencies, fast for small datasets
- **Cons**: Data doesn't persist between runs
- **Use case**: Development, testing, small knowledge bases

### PostgreSQL with pgvector

- **Use case**: Production deployments, persistent storage
- **Setup**: Requires PostgreSQL with pgvector extension
- **Environment Variables**:
  ```bash
  export PGVECTOR_HOST="127.0.0.1"
  export PGVECTOR_PORT="5432"
  export PGVECTOR_USER="postgres"
  export PGVECTOR_PASSWORD="your_password"
  export PGVECTOR_DATABASE="vectordb"
  ```

### TcVector

- **Use case**: Cloud deployments, managed vector storage
- **Setup**: Requires TcVector service credentials
- **Environment Variables**:
  ```bash
  export TCVECTOR_URL="your_tcvector_service_url"
  export TCVECTOR_USERNAME="your_username"
  export TCVECTOR_PASSWORD="your_password"
  ```

## Embedder Options

### OpenAI Embedder (Default)

- **Model**: `text-embedding-3-small` (configurable)
- **Environment Variable**: `OPENAI_EMBEDDING_MODEL`
- **Use case**: High-quality embeddings with OpenAI's latest models

### Gemini Embedder

- **Model**: Uses Gemini's default embedding model
- **Use case**: Alternative to OpenAI, good for Google ecosystem integration

## Configuration

### Required Environment Variables

#### For OpenAI (Model + Embedder)

```bash
export OPENAI_API_KEY="your-openai-api-key"           # Required for OpenAI model and embedder
export OPENAI_BASE_URL="your-openai-base-url"    # Required for OpenAI model and embedder
export OPENAI_EMBEDDING_MODEL="text-embedding-3-small" # Required for OpenAI embedder only
```

#### For Gemini Embedder

```bash
export GOOGLE_API_KEY="your-google-api-key"  # Only this is needed for Gemini embedder
```

### Optional Configuration

- Vector store specific variables (see vector store documentation for details)

### Command Line Options

```bash
-model string       LLM model name (default: "claude-4-sonnet-20250514")
-streaming bool     Enable streaming mode for responses (default: true)
-embedder string    Embedder type: openai, gemini (default: "openai")
-vectorstore string Vector store type: inmemory, pgvector, tcvector (default: "inmemory")
```

---

For more details, see the code in `main.go`.

## How It Works

### 1. Knowledge Base Setup

The example creates a knowledge base with configurable vector store and embedder:

```go
// Create knowledge base with configurable components
vectorStore, err := c.setupVectorDB() // Supports inmemory, pgvector, tcvector
embedder, err := c.setupEmbedder(ctx) // Supports openai, gemini

kb := knowledge.New(
    knowledge.WithVectorStore(vectorStore),
    knowledge.WithEmbedder(embedder),
    knowledge.WithSources(sources),
)

// Load the knowledge base with optimized settings
if err := kb.Load(
    ctx,
    knowledge.WithShowProgress(false),  // Disable progress logging
    knowledge.WithShowStats(false),     // Disable statistics display
    knowledge.WithSourceConcurrency(4), // Process 4 sources concurrently
    knowledge.WithDocConcurrency(64),   // Process 64 documents concurrently
); err != nil {
    return fmt.Errorf("failed to load knowledge base: %w", err)
}
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
    llmagent.WithDescription("A helpful AI assistant with knowledge base access."),
    llmagent.WithInstruction("Use the knowledge_search tool to find relevant information from the knowledge base. Be helpful and conversational."),
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

- **Storage**: Configurable vector store (in-memory, pgvector, or tcvector)
- **Vector Store**: Vector similarity search with multiple backends
- **Embedder**: OpenAI or Gemini embedder for document representation
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
- **Embedders**:
  - `embedder/openai`: OpenAI embeddings API integration
  - `embedder/gemini`: Gemini embeddings API integration
- **Sources**: `source/{file,dir,url,auto}`: Multiple content source types
- **Session**: `session/inmemory`: In-memory conversation state management
- **Runner**: Multi-turn conversation management with streaming support

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
- Consider using environment-specific configuration files

## Example Files

| File                  | Description                                                            |
| --------------------- | ---------------------------------------------------------------------- |
| `main.go`             | Complete knowledge integration example with multi-vector store support |
| `data/llm.md`         | Sample documentation about Large Language Models                       |
| `dir/transformer.pdf` | Transformer architecture documentation                                 |
| `dir/moe.txt`         | Mixture-of-Experts model notes                                         |
| `README.md`           | This comprehensive documentation                                       |

## Key Dependencies

- `agent/llmagent`: LLM agent with streaming and tool support
- `knowledge/*`: Complete RAG pipeline with multiple source types
- `knowledge/vectorstore/*`: Multiple vector storage backends
- `knowledge/embedder/*`: Multiple embedder implementations
- `runner`: Multi-turn conversation management with session state
- `session/inmemory`: In-memory session state management

## Troubleshooting

### Common Issues

1. **OpenAI API Key Error**

   - Ensure `OPENAI_API_KEY` is set correctly
   - Verify your OpenAI account has embedding API access
   - **Important**: For OpenAI model and embedder, you must also set `OPENAI_BASE_URL` and `OPENAI_EMBEDDING_MODEL`

2. **Gemini API Key Error**

   - Ensure `GOOGLE_API_KEY` is set correctly
   - Verify your Google Cloud project has Gemini API access enabled
   - **Note**: Gemini embedder only requires `GOOGLE_API_KEY`, no additional model configuration needed

3. **Vector Store Connection Issues**

   - For pgvector: Ensure PostgreSQL is running and pgvector extension is installed
   - For tcvector: Verify service credentials and network connectivity
   - Check environment variables are set correctly

4. **Knowledge Loading Errors**

   - Verify source files/URLs are accessible
   - Check file permissions for local sources
   - Ensure stable internet connection for URL sources

5. **Embedder Configuration Issues**

   - **OpenAI**: Verify `OPENAI_API_KEY`, `OPENAI_BASE_URL`, and `OPENAI_EMBEDDING_MODEL` are all set
   - **Gemini**: Verify `GOOGLE_API_KEY` is set and API is enabled
   - Check API quotas and rate limits for both services
