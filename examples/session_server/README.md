# Session Server Example

This example demonstrates how to use the session management and streaming features of the tRPC Agent Framework. It creates a simple HTTP server that provides a RESTful API for multi-turn chat conversations with a language model.

## Usage

Inside one terminal, run the session server:

```bash
export OPENAI_API_KEY=your_openai_api_key_here
export OPENAI_BASE_URL=https://your-openai-url
export OPENAI_MODEL_NAME=deepseek-v3
go run . 
```

In another terminal, run the client:

```bash
$ go run cmd/client/main.go 
Creating new session...
Session created: 89b39c8fe23a4bf25b1320fde7aae1f0
Enter your messages (type 'quit' to exit):

You:
```

You can shutdown the client and then resume the session by running the client again with the same session ID gotten from the previous run:

```bash
$ go run cmd/client/main.go -session=your_session_id
```
