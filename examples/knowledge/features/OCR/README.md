# PDF OCR Knowledge Demo

Demonstrates PDF OCR capability with knowledge base integration.

## Features

- PDF document reading (text and images)
- OCR text extraction (Tesseract)
- Automatic document chunking
- Vector storage (inmemory, pgvector, tcvector, elasticsearch)
- Semantic search and retrieval

## Prerequisites

### 1. Tesseract OCR Engine

```bash
# Ubuntu/Debian
sudo apt-get install tesseract-ocr libtesseract-dev tesseract-ocr-chi-sim

# macOS
brew install tesseract
```

### 2. OpenAI API Configuration

```bash
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"  # Optional
```

## Run

```bash
# Prepare PDF files in ./data directory
mkdir -p ./data
cp /path/to/your/*.pdf ./data/

# Run with in-memory vector store (default)
go run -tags tesseract main.go

# Run with other vector stores
go run -tags tesseract main.go -vectorstore pgvector

# Custom query
go run -tags tesseract main.go -query "What is trpc-agent-go?"
```

## Command-Line Options

| Option | Description | Default |
|--------|-------------|---------|
| `-vectorstore` | Vector store type: inmemory\|pgvector\|tcvector\|elasticsearch | inmemory |
| `-query` | Search query | "What is trpc-agent-go?" |

## Example Output

```
PDF OCR Knowledge Demo
==================================================
Data Directory: ./data
OCR Engine: Tesseract
Vector Store: inmemory
==================================================

Setting up knowledge base...
  Creating Tesseract OCR engine...
  Creating OpenAI embedder...
  Creating vector store...
  Creating directory source for PDFs in /path/to/data...
  Creating knowledge base...

Loading PDFs into knowledge base...
Knowledge base loaded successfully in 5.2s

📊 Knowledge Base Statistics
--------------------------------------------------
  Total Chunks: 42
  Source Files: 1
  OCR-Processed Chunks: 12
  Total Characters: 28500
  Avg Chars/Chunk: 678
  OCR Engine: Tesseract
  Vector Store: inmemory

🔍 Query: What is trpc-agent-go?
--------------------------------------------------
Search completed in 120ms
Found 3 results:

  #1 (Score: 0.8234)
    Source: trpc-agent-go.pdf
    Metadata: type=pdf, ocr_enabled=true
    Content: trpc-agent-go is an AI agent framework...

✅ Done!
```
