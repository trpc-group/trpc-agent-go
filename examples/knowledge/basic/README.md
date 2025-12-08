# Basic Knowledge Example

This is the simplest example to get started with knowledge-enhanced chat using trpc-agent-go.

## What it demonstrates

- Single file source
- In-memory vector store
- OpenAI embedder
- Basic question answering
- Command-line query option

## Prerequisites

Set your OpenAI API key:

```bash
export OPENAI_API_KEY=your-api-key
export OPENAI_BASE_URL=https://api.openai.com/v1  # Optional
export MODEL_NAME=deepseek-chat                    # Optional, defaults to deepseek-chat
```

## Run

### With default query

```bash
go run main.go
```

### With custom query

```bash
go run main.go -query "What is a Large Language Model?"
```

### Choose vector store

```bash
go run main.go -vectorstore pgvector   # defaults to inmemory
```

## Example questions

- "What is a Large Language Model?"
- "How do LLMs work?"
- "What are transformers?"

## Next steps

- **sources/**: Learn about different data sources (file, directory, URL, auto)
- **vectorstores/**: Explore persistent storage options (PostgreSQL, Elasticsearch)
- **features/**: Discover advanced features (agentic filter, streaming, metadata)
