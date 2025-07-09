# Knowledge Integration Example

This example demonstrates how to integrate a knowledge base with the LLM agent in `trpc-agent-go`.

## Features

- Uses the built-in knowledge system with in-memory storage and vector store.
- Integrates the OpenAI embedder for generating embeddings for knowledge documents.
- Shows how to add documents to the knowledge base and enable knowledge search in the agent.
- Provides a chat interface with knowledge search, calculator, and current time tools.

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

1. **Configure the OpenAI Embedder**

   The example uses the OpenAI embedder from `core/knowledge/embedder/openai`. You must provide a valid OpenAI API key for embedding. In the code, replace:

   ```go
   embedder := openaiencoder.New()
   ```

   with your actual OpenAI API key.

2. **Run the Example**

   ```sh
   cd examples/knowledge
   go run main.go
   ```

3. **Interact with the Chat**

   - Type your questions to interact with the knowledge-enhanced agent.
   - Use `exit` to quit the chat.

## Tools Available

- `knowledge_search`: Search for relevant information in the knowledge base.
- `calculator`: Perform basic mathematical calculations.
- `current_time`: Get the current time and date for a specific timezone.

## Notes

- The knowledge base is initialized with several sample documents about machine learning, Python, data science, web development, and cloud computing.
- The OpenAI embedder is required for knowledge search to work. Make sure your API key is valid and has embedding access.

---

For more details, see the code in `main.go`.

## How It Works

### 1. Knowledge Base Setup

The example creates a built-in knowledge base with in-memory components:

```go
// Create in-memory storage and vector store
storage := storageinmemory.New()
vectorStore := vectorinmemory.New()

// Create OpenAI embedder for demonstration
embedder := openaiembedder.New(
    openaiembedder.WithAPIKey("sk-your-openai-key"),
)

// Create built-in knowledge base
kb := knowledge.New(
    knowledge.WithStorage(storage),
    knowledge.WithVectorStore(vectorStore),
    knowledge.WithEmbedder(embedder),
)
```

### 2. Document Creation

Documents are created using the document builder:

```go
doc := builder.FromText(
    "Machine learning is a subset of artificial intelligence...",
    builder.WithTextID("machine-learning"),
    builder.WithTextName("Machine Learning Basics"),
)
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

- **storage/inmemory**: In-memory document storage implementation
- **vectorstore/inmemory**: In-memory vector store with cosine similarity
- **embedder/mock**: Mock embedder for demonstration
- **document/builder**: Document creation utilities

## Extending the Example

To use your own knowledge base:

1. Implement the `knowledge.Knowledge` interface or use `BuiltinKnowledge`
2. Provide your own storage, vector store, and embedder implementations
3. Add your documents using `AddDocument()`
4. Pass your knowledge base to the agent using `WithKnowledge()`

For production use, consider:
- Using persistent storage (database, file system)
- Using production vector stores (Pinecone, Weaviate, etc.)
- Using real embedding models (OpenAI, Cohere, etc.)

## Files

- `main.go`: Complete example implementation
- `README.md`: This documentation file

## Dependencies

- `trpc-agent-go/core/agent/llmagent`: LLM agent implementation
- `trpc-agent-go/core/knowledge`: Knowledge management interfaces
- `trpc-agent-go/core/knowledge/tool`: Knowledge search tool
- `trpc-agent-go/orchestration/runner`: Multi-turn conversation runner 