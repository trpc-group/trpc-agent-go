# Session Server Example

This example demonstrates how to use the session management and streaming features of the tRPC Agent Framework. It creates a simple HTTP server that provides a RESTful API for multi-turn chat conversations with a language model.

## Features

- Session management for persistent conversations
- Streaming responses via Server-Sent Events (SSE)
- In-memory or file-based session storage
- Simple calculator tool integration

## Requirements

- Go 1.19 or higher
- An OpenAI API key

## Usage

Inside one terminal, run the session server:

```bash
export OPENAI_API_KEY=your_openai_api_key_here
go run . -openai-url="https://your-openai-url" --model-name="deepseek-v3" 
```

In another terminal, run the client:

```bash
go run cmd/client/main.go 
```
