# PDF OCR Knowledge Demo

This example demonstrates how to use trpc-agent-go's Knowledge module with OCR capabilities to process PDF documents and perform vector storage and retrieval using TCVector.

## Features

- ✅ PDF document reading (supports text and images)
- ✅ OCR text extraction (Tesseract)
- ✅ Automatic document chunking
- ✅ Vector storage with TCVector
- ✅ Semantic search and retrieval
- ✅ Interactive query interface

## Quick Start

```bash
# 1. Set required environment variables
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"
export TCVECTOR_URL="http://your-tcvector-host:port"
export TCVECTOR_USERNAME="your-username"
export TCVECTOR_PASSWORD="your-password"

# 2. Prepare PDF files
mkdir -p ./data
cp /path/to/your/*.pdf ./data/

# 3. Install Tesseract OCR
# Ubuntu/Debian:
sudo apt-get install tesseract-ocr libtesseract-dev

# macOS:
brew install tesseract

# 4. Run the example
cd examples/knowledge/OCR
go run main.go
```

## Prerequisites

### 1. OpenAI API Configuration (Required)

This example uses OpenAI Embedder for text vectorization. You need to configure the following environment variables:

```bash
# OpenAI API Key (Required)
export OPENAI_API_KEY="your-openai-api-key"

# OpenAI API Base URL (Required)
# For official OpenAI API:
export OPENAI_BASE_URL="https://api.openai.com/v1"

# For compatible third-party services (e.g., Azure OpenAI, local deployment):
export OPENAI_BASE_URL="https://your-custom-endpoint/v1"
```

**Note**:
- Both `OPENAI_API_KEY` and `OPENAI_BASE_URL` are required environment variables
- The default embedding model is `text-embedding-3-small`
- Ensure your API endpoint supports this model

### 2. TCVector Configuration

You need a running TCVector instance with the following information:
- TCVector URL
- Username
- Password

Configure via environment variables or command-line parameters:
```bash
export TCVECTOR_URL="http://your-tcvector-host:port"
export TCVECTOR_USERNAME="your-username"
export TCVECTOR_PASSWORD="your-password"
```

### 3. Tesseract OCR Engine

Install Tesseract OCR:
```bash
# Ubuntu/Debian
sudo apt-get update
sudo apt-get install tesseract-ocr libtesseract-dev

# Install Chinese language pack (optional)
sudo apt-get install tesseract-ocr-chi-sim

# macOS
brew install tesseract

# Verify installation
tesseract --version
```

## Installation

```bash
cd examples/knowledge/OCR
go mod tidy
```

## Usage

### Prepare Data

Place PDF files in the `./data` directory (or specify another directory with `--data`):

```bash
mkdir -p ./data
cp /path/to/your/*.pdf ./data/
```

### Configure Environment Variables

Before running the program, ensure you have set the required environment variables:

```bash
# OpenAI API Configuration (Required)
export OPENAI_API_KEY="your-openai-api-key"
export OPENAI_BASE_URL="https://api.openai.com/v1"

# TCVector Configuration (Optional, can also be specified via command-line parameters)
export TCVECTOR_URL="http://your-tcvector-host:port"
export TCVECTOR_USERNAME="your-username"
export TCVECTOR_PASSWORD="your-password"
```

### Basic Usage

```bash
# Ensure OPENAI_API_KEY and OPENAI_BASE_URL are set
go run main.go \
  --data=./data \
  --tcvector-url=$TCVECTOR_URL \
  --tcvector-user=$TCVECTOR_USERNAME \
  --tcvector-pass=$TCVECTOR_PASSWORD
```

### Recreate Vector Store

To clear existing data and reload:
```bash
go run main.go \
  --data=./data \
  --tcvector-url=$TCVECTOR_URL \
  --tcvector-user=$TCVECTOR_USERNAME \
  --tcvector-pass=$TCVECTOR_PASSWORD \
  --recreate
```

### Using the Convenience Script

The project provides a `run_example.sh` script to simplify execution:

```bash
# Edit the script to set environment variables
vim run_example.sh

# Run the example
./run_example.sh
```

## Command-Line Parameters

| Parameter | Description | Default | Environment Variable | Required |
|-----------|-------------|---------|---------------------|----------|
| `--data` | PDF files directory | ./data | - | ❌ |
| `--tcvector-url` | TCVector service URL | - | `TCVECTOR_URL` | ✅ |
| `--tcvector-user` | TCVector username | - | `TCVECTOR_USERNAME` | ✅ |
| `--tcvector-pass` | TCVector password | - | `TCVECTOR_PASSWORD` | ✅ |
| `--recreate` | Recreate vector store | true | - | ❌ |

### Environment Variables (Required)

| Environment Variable | Description | Example Value | Required |
|---------------------|-------------|---------------|----------|
| `OPENAI_API_KEY` | OpenAI API key | `sk-...` | ✅ |
| `OPENAI_BASE_URL` | OpenAI API base URL | `https://api.openai.com/v1` | ✅ |
| `TCVECTOR_URL` | TCVector service URL | `http://localhost:8080` | ✅ (or via parameter) |
| `TCVECTOR_USERNAME` | TCVector username | `admin` | ✅ (or via parameter) |
| `TCVECTOR_PASSWORD` | TCVector password | `password` | ✅ (or via parameter) |

## Interactive Commands

After running, the program enters interactive query mode with the following commands:

- **Direct input**: Perform semantic search in PDF content
- **/stats**: Show knowledge base statistics
- **/exit**: Exit the program

## Example Session

```
📄 PDF OCR Knowledge Demo
==============================================================
Data Directory: ./data
Vector Store: TCVector
Collection: pdf-ocr-1
==============================================================

🔧 Setting up knowledge base...
   Creating Tesseract OCR engine...
   Creating OpenAI embedder...
   Creating TCVector store...
   Creating directory source for PDFs in /path/to/data...
   Creating knowledge base...

📚 Loading PDFs into knowledge base...
Progress: 100% | Time: 15.2s | Docs: 25
✅ Knowledge base loaded successfully in 15.2s

🔍 PDF Search Interface
==============================================================
💡 Commands:
   /exit     - Exit the program
   /stats    - Show knowledge base statistics

🎯 Try searching for content in your PDF:
   - Enter any keywords or questions
   - Search results will show matching text chunks

🔍 Query: What is machine learning?

🔎 Searching for: "What is machine learning?"
⏱️  Search completed in 234ms
📊 Found 5 results:
-------------------------------------------------------------

📄 Result #1 (Score: 0.8542)
   Source: research_paper.pdf
   Metadata: type=pdf, ocr_enabled=true, chunk_index=3
   Content: Machine learning is a subset of artificial intelligence
   that enables computers to learn from data without being explicitly
   programmed. It involves algorithms that can identify patterns...

📄 Result #2 (Score: 0.7891)
   Source: research_paper.pdf
   Metadata: type=pdf, ocr_enabled=true, chunk_index=7
   Content: Deep learning, a branch of machine learning, uses neural
   networks with multiple layers to process complex data...

🔍 Query: /stats

📊 Knowledge Base Statistics
-------------------------------------------------------------
   Total Documents: 25
   OCR-Processed: 25
   Total Characters: 45623
   Avg Chars/Doc: 1825
   Vector Store: TCVector
   Collection: pdf-ocr-1

🔍 Query: /exit
👋 Goodbye!
```

## Workflow

1. **PDF Loading**: Read specified PDF files
2. **OCR Processing**:
   - Extract text layer content from PDF
   - Perform OCR on embedded images
   - Merge text and OCR results
   - Mark OCR content with `[OCR Image - Page X, Image Y]` tags
3. **Document Chunking**: Split long documents into appropriately sized chunks
4. **Vectorization**: Convert text to vectors using OpenAI Embedding API
5. **Storage**: Store document vectors in TCVector
6. **Retrieval**: Perform semantic search based on queries and return relevant results

## Technical Architecture

```
┌─────────────┐
│  PDF File   │
└──────┬──────┘
       │
       ▼
┌─────────────────────┐
│   PDF Reader        │
│  - Text Extraction  │
│  - Image Extraction │
└──────┬──────────────┘
       │
       ▼
┌─────────────────────┐
│   OCR Engine        │
│  - Tesseract        │
└──────┬──────────────┘
       │
       ▼
┌─────────────────────┐
│   Chunking          │
│  - Fixed Size       │
└──────┬──────────────┘
       │
       ▼
┌─────────────────────┐
│   Embedder          │
│  - OpenAI API       │
└──────┬──────────────┘
       │
       ▼
┌─────────────────────┐
│   TCVector Store    │
│  - Vector Storage   │
│  - Similarity Search│
└─────────────────────┘
```

## Performance Optimization Tips

1. **Document Chunking**:
   - Default chunk size: 1024 tokens
   - Adjust based on your use case

2. **Batch Processing**:
   - Use batch loading for multiple PDFs
   - Set appropriate concurrency levels

3. **Caching Strategy**:
   - Use `--recreate=false` to avoid reloading
   - TCVector automatically caches vectors

4. **OCR Quality**:
   - Ensure PDF images have high resolution
   - Install appropriate language packs for Tesseract
   - Adjust confidence threshold if needed

### Use Other Vector Stores

Replace TCVector with other supported vector stores:
- InMemory (development/testing)
- PGVector (PostgreSQL)
- Elasticsearch


## Related Documentation

- [Knowledge Module Documentation](../../../knowledge/README.md)
- [OCR Module Documentation](../../../knowledge/ocr/README.md)
- [PDF Reader Documentation](../../../knowledge/document/reader/pdf/README.md)
- [TCVector Integration Documentation](../../../knowledge/vectorstore/tcvector/README.md)
