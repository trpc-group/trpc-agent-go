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
- **Performance tuning**: The knowledge package provides intelligent defaults for concurrency. Adjust `WithSourceConcurrency()` and `WithDocConcurrency()` based on your specific needs:
  - **Speed up**: Increase values if processing is too slow and API limits allow
  - **Slow down**: Decrease values if hitting API rate limits or experiencing errors
  - **Default**: Use default values for balanced performance (recommended for most cases)
- **Loading behavior**: Control progress logging, statistics display, and update frequency:
  - `WithShowProgress(false)`: Disable progress logging (default: true)
  - `WithShowStats(false)`: Disable statistics display (default: true)
  - `WithProgressStepSize(10)`: Set progress update frequency (default: 10)

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
    knowledge.WithShowProgress(false),  // Disable progress logging (default: true)
    knowledge.WithProgressStepSize(10), // Progress update frequency (default: 10)
    knowledge.WithShowStats(false),     // Disable statistics display (default: true)
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

### Knowledge Loading Configuration

The example configures several loading options to optimize the user experience:

```go
// Current configuration (overrides defaults)
if err := c.kb.Load(
    ctx,
    knowledge.WithShowProgress(false),  // Disable progress logging (default: true)
    knowledge.WithProgressStepSize(10), // Progress update frequency (default: 10)
    knowledge.WithShowStats(false),     // Disable statistics display (default: true)
    knowledge.WithSourceConcurrency(4), // Override default: min(4, len(sources))
    knowledge.WithDocConcurrency(64),   // Override default: runtime.NumCPU()
); err != nil {
    return fmt.Errorf("failed to load knowledge base: %w", err)
}
```

**Available Options:**

- **Progress Control**: `WithShowProgress()` - Enable/disable progress logging
- **Update Frequency**: `WithProgressStepSize()` - Control progress update intervals
- **Statistics Display**: `WithShowStats()` - Show/hide loading statistics
- **Concurrency Tuning**: `WithSourceConcurrency()` and `WithDocConcurrency()` - Performance optimization

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

### Performance Optimization & API Rate Limits

The example uses parallel processing to optimize knowledge base loading. The knowledge package provides intelligent defaults:

```go
// Current configuration (overrides defaults)
knowledge.WithShowProgress(false), // Disable progress logging
knowledge.WithSourceConcurrency(4), // Override default: min(4, len(sources))
knowledge.WithDocConcurrency(64),   // Override default: runtime.NumCPU()
```

**⚠️ Important Notes:**

- **Increased API Calls**: Parallel processing will significantly increase the frequency of API requests to your embedder service (OpenAI/Gemini)
- **Rate Limit Considerations**: Be aware of your API provider's rate limits and quotas
- **Cost Impact**: More concurrent requests may increase costs for paid API services
- **Adjust as Needed**: Balance between processing speed and API rate limits

**Performance vs. Rate Limits - Finding the Sweet Spot:**

The concurrency settings affect both processing speed and API request frequency. You need to find the right balance:

- **Too Slow?** Increase concurrency for faster processing
- **Too Fast?** Reduce concurrency to avoid hitting API rate limits
- **Just Right?** Default values are optimized for most scenarios

**When to Adjust Concurrency:**

```go
// If processing is too slow (acceptable API usage)
knowledge.WithSourceConcurrency(8),  // Increase from default 4
knowledge.WithDocConcurrency(128),   // Increase from default runtime.NumCPU()

// If hitting API rate limits (slower but stable)
knowledge.WithSourceConcurrency(2),  // Reduce from default 4
knowledge.WithDocConcurrency(16),    // Reduce from default runtime.NumCPU()

// Default values (balanced approach)
// knowledge.WithSourceConcurrency() // Default: min(4, len(sources))
// knowledge.WithDocConcurrency()     // Default: runtime.NumCPU()
```

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

6. **API Rate Limiting & Parallel Processing Issues**

   - **Rate Limit Errors**: If you see "rate limit exceeded" errors, reduce concurrency settings to slow down API requests
   - **High API Costs**: Parallel processing increases API call frequency - the default values are optimized for most use cases
   - **Slow Loading**: If knowledge base loading is too slow, increase concurrency (if API limits allow) for faster processing
   - **Memory Usage**: High concurrency may increase memory usage during loading - defaults are balanced for performance and resource usage
   - **Performance Tuning**: Monitor loading time vs. API errors to find optimal concurrency settings for your environment

7. **Knowledge Loading Behavior Issues**

   - **Need More Visibility**: If you want to see loading progress, enable `WithShowProgress(true)` and `WithShowStats(true)`
   - **Too Many Logs**: If progress logs are too verbose, increase `WithProgressStepSize()` value or disable with `WithShowProgress(false)`
   - **Missing Statistics**: If you need document size and count information, enable `WithShowStats(true)`
   - **Custom Progress Frequency**: Adjust `WithProgressStepSize()` to control how often progress updates are logged (default: 10)
