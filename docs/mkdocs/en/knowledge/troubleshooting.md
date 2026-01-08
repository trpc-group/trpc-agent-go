# Troubleshooting

## Common Issues and Solutions

### 1. Create embedding failed/HTTP 4xx/5xx

**Possible Causes**:
- Invalid or missing API Key
- Incorrect BaseURL configuration
- Network access restricted
- Text too long
- Configured BaseURL doesn't provide Embeddings endpoint or doesn't support the selected embedding model (e.g., returns 404 Not Found)

**Troubleshooting Steps**:
1. Confirm `OPENAI_API_KEY` is set and valid
2. If using a compatible gateway, explicitly set `WithBaseURL(os.Getenv("OPENAI_BASE_URL"))`
3. Confirm `WithModel("text-embedding-3-small")` or the embedding model name actually supported by your service
4. Use a minimal example to call the embedding API once to verify connectivity
5. Use curl to verify the target BaseURL implements `/v1/embeddings` and the model exists:
   ```bash
   curl -sS -X POST "$OPENAI_BASE_URL/embeddings" \
     -H "Authorization: Bearer $OPENAI_API_KEY" \
     -H "Content-Type: application/json" \
     -d '{"model":"text-embedding-3-small","input":"ping"}'
   ```
   If it returns 404/model doesn't exist, switch to a BaseURL that supports Embeddings or use a valid embedding model name provided by that service
6. Gradually shorten text to confirm it's not caused by overly long input

**Reference Code**:
```go
import (
    "log"
    "os"

    openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
)

embedder := openaiembedder.New(
    openaiembedder.WithModel("text-embedding-3-small"),
    openaiembedder.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
    openaiembedder.WithBaseURL(os.Getenv("OPENAI_BASE_URL")),
)
if _, err := embedder.GetEmbedding(ctx, "ping"); err != nil {
    log.Fatalf("embed check failed: %v", err)
}
```


### 2. PDF File Reading Support

**Description**: Since the PDF reader depends on third-party libraries, to avoid introducing unnecessary dependencies in the main module, the PDF reader uses a separate `go.mod`.

**Usage**: To support PDF file reading, manually import the PDF reader package in your code:
```go
import (
    // Import PDF reader to support .pdf file parsing
    _ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)
```


## Environment Variables for Running Examples

> The following environment variables are used for running example programs in the [examples/knowledge](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/knowledge) directory.

```bash
# OpenAI API configuration (required when using OpenAI embedder, auto-read by OpenAI SDK)
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="your-openai-base-url"
# OpenAI embedding model configuration (optional, requires manual reading in code)
export OPENAI_EMBEDDING_MODEL="text-embedding-3-small"

# Google Gemini API configuration (when using Gemini embedder)
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

# Milvus configuration (required when using -vectorstore=milvus)
export MILVUS_ADDRESS="localhost:19530"
export MILVUS_USERNAME=""
export MILVUS_PASSWORD=""
export MILVUS_DB_NAME=""
export MILVUS_COLLECTION="trpc_agent_go"
```
