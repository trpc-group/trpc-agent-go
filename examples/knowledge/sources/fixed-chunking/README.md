# Fixed-Size Chunking Example

Demonstrates how to use fixed-size chunking strategy for document splitting.

## Features

- Fixed-size text chunking with configurable chunk size
- Configurable overlap between consecutive chunks
- Preview of chunking results before loading to the knowledge base
- UTF-8 safe text splitting

## Chunking Strategy

`FixedSizeChunking` splits text into fixed-size chunks:
- `WithChunkSize(100)`: Maximum 100 characters per chunk
- `WithOverlap(10)`: 10 characters overlap between chunks

## Run

```bash
export OPENAI_BASE_URL=xxx
export OPENAI_API_KEY=xxx
export MODEL_NAME=xxx
go run main.go
```

## Output

The example will:
1. Display chunking preview with chunk IDs, sizes, and content previews
2. Load documents into knowledge base
3. Run a sample query using the chunked documents
