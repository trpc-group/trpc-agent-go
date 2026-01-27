# Recursive Chunking Example

Demonstrates how to use recursive chunking strategy for intelligent document splitting.

## Features

- Recursive text chunking with separator hierarchy
- Configurable chunk size and overlap
- Custom separators for intelligent text splitting
- Preview of chunking results before loading to knowledge base

## Chunking Strategy

`RecursiveChunking` uses a hierarchy of separators to split text intelligently:
- `WithRecursiveChunkSize(512)`: Maximum 512 characters per chunk
- `WithRecursiveOverlap(64)`: 64 characters overlap between chunks
- `WithRecursiveSeparators([]string{"\n\n", "\n", ". ", " ", ""})`: Split priority

### Separator Priority

1. `\n\n` - Split by paragraph
2. `\n` - Split by line
3. `. ` - Split by sentence
4. ` ` - Split by space
5. `` - Split by character (fallback)

This ensures text is split at natural boundaries when possible.

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
3. Run sample queries using the chunked documents
