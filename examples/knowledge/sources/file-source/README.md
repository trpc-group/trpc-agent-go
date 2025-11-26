# File Source Example

Demonstrates how to use file sources with metadata.

## Features

- Multiple file sources
- Custom metadata (category, format)
- Support for different file formats (Markdown, PDF, etc.)

## Run

```bash
export OPENAI_BASE_URL=xxx
export OPENAI_API_KEY=xxx
export MODEL_NAME=xxx
go run main.go
```

## Supported formats

- Markdown (`.md`)
- PDF (`.pdf`) - requires PDF reader import
- Text (`.txt`)
- CSV (`.csv`)
- JSON (`.json`)
- DOCX (`.docx`)
