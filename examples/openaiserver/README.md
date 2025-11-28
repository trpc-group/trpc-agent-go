# OpenAI Server Example

This example shows how to start the trpc-agent-go **OpenAI-compatible Server** that
implements the OpenAI Chat Completions API standard.

## Prerequisites

- Go 1.21 or later
- OpenAI API key (or compatible API key for the model you're using)

## Running the Server

```bash
# From repository root
cd examples/openaiserver

# Start the server with default settings (model: deepseek-chat, addr: :8080)
go run .

# Start the server on custom port
go run . -addr :9090

# Start the server with different model
go run . -model gpt-4

# Start the server with custom model and port
go run . -model gpt-4 -addr :9090
```

### Command Line Options

- `-model`: Name of the model to use (default: "deepseek-chat")
- `-addr`: Listen address (default: ":8080")

## Testing with curl

### Non-streaming Request

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "messages": [
      {"role": "user", "content": "What is 2 + 2?"}
    ],
    "stream": false
  }'
```

### Streaming Request

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "messages": [
      {"role": "user", "content": "What is 2 + 2?"}
    ],
    "stream": true
  }'
```

### Using Tools (Function Calling)

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "messages": [
      {"role": "user", "content": "Calculate 15 * 23"}
    ],
    "stream": false
  }'
```

## Testing with OpenAI SDK

You can use any OpenAI-compatible client library. Here's an example with Python:

```python
from openai import OpenAI

# Point to your local server
client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key="not-needed"  # API key is not required for local server
)

# Non-streaming
response = client.chat.completions.create(
    model="deepseek-chat",
    messages=[
        {"role": "user", "content": "What is 2 + 2?"}
    ]
)
print(response.choices[0].message.content)

# Streaming
stream = client.chat.completions.create(
    model="deepseek-chat",
    messages=[
        {"role": "user", "content": "Tell me a story"}
    ],
    stream=True
)
for chunk in stream:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="", flush=True)
```

## Features

- ✅ OpenAI Chat Completions API compatible
- ✅ Streaming and non-streaming responses
- ✅ Function calling (tools) support
- ✅ Multi-turn conversations
- ✅ Session management

## Available Tools

- **calculator**: Performs basic mathematical operations (add, subtract, multiply, divide)
- **current_time**: Gets the current time and date for a specific timezone

---

Feel free to replace the agent logic in `main.go` or add more tools in `tools.go` as needed.

