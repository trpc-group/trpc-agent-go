# Auto Source Example

Demonstrates automatic content type detection for mixed inputs.

## Features

- Automatic type detection (text/file/URL)
- Mixed content handling
- Simplified API for diverse sources

## Run

```bash
export OPENAI_BASE_URL=xxx
export OPENAI_API_KEY=xxx
export MODEL_NAME=xxx
go run main.go
```

## How it works

Auto source automatically determines if the input is:
- Plain text content
- File path (local file)
- URL (web page)

This simplifies handling multiple content types without manual classification.
