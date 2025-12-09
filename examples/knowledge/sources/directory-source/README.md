# Directory Source Example

Demonstrates how to load entire directories of documents.

## Features

- Recursive directory loading
- Automatic file type detection
- Batch processing of multiple files
- Progress tracking

## Run

```bash
export OPENAI_BASE_URL=xxx
export OPENAI_API_KEY=xxx
export MODEL_NAME=xxx
go run main.go
```

## How it works

The directory source recursively scans the specified directory and processes all supported file formats automatically.
