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

### Building

```bash
cd examples/session_server
go build -o session_server
```

### Running

```bash
# Run with in-memory storage
./session_server -api_key=your_openai_api_key

# Or use an environment variable for the API key
export OPENAI_API_KEY=your_openai_api_key
./session_server

# Run with file-based storage
./session_server -session_dir=./sessions
```

### Command Line Options

| Flag | Description | Default |
|------|-------------|---------|
| `-port` | HTTP server port | 8080 |
| `-api_key` | OpenAI API key (can also use OPENAI_API_KEY env var) | - |
| `-model` | OpenAI model name | gpt-3.5-turbo |
| `-stream` | Enable streaming responses | true |
| `-session_dir` | Directory for session storage. If empty, uses in-memory storage | - |

## API Endpoints

### Create a Session

```
POST /sessions
```

Example:

```bash
curl -X POST http://localhost:8080/sessions
```

Response:

```json
{
  "id": "session-123456"
}
```

### List Sessions

```
GET /sessions
```

Example:

```bash
curl http://localhost:8080/sessions
```

Response:

```json
{
  "sessions": ["session-123456", "session-789012"]
}
```

### Get Session Info

```
GET /sessions/{id}
```

Example:

```bash
curl http://localhost:8080/sessions/session-123456
```

Response:

```json
{
  "id": "session-123456",
  "messages": [...],
  "updated_at": "2023-06-01T12:34:56Z"
}
```

### Delete a Session

```
DELETE /sessions/{id}
```

Example:

```bash
curl -X DELETE http://localhost:8080/sessions/session-123456
```

Response:

```json
{
  "success": true,
  "message": "Session session-123456 deleted"
}
```

### Run Agent (Non-Streaming)

```
POST /sessions/{id}/run
```

Example:

```bash
curl -X POST http://localhost:8080/sessions/session-123456/run \
  -H "Content-Type: application/json" \
  -d '{"message":"What is 2+2?"}'
```

Response:

```json
{
  "message": {
    "id": "msg-123456",
    "role": "assistant",
    "content": "2+2 equals 4."
  }
}
```

### Run Agent (Streaming)

```
POST /sessions/{id}/run_stream
```

Example:

```bash
curl -X POST http://localhost:8080/sessions/session-123456/run_stream \
  -H "Content-Type: application/json" \
  -d '{"message":"What is 2+2?"}' \
  -N
```

Response (Server-Sent Events):

```
data: {"type":"stream_start","id":"evt-123456","created_at":"2023-06-01T12:34:56Z"}

data: {"type":"stream_chunk","id":"evt-123457","created_at":"2023-06-01T12:34:56Z","data":{"content":"2","sequence":1}}

data: {"type":"stream_chunk","id":"evt-123458","created_at":"2023-06-01T12:34:56Z","data":{"content":"+","sequence":2}}

data: {"type":"stream_chunk","id":"evt-123459","created_at":"2023-06-01T12:34:56Z","data":{"content":"2","sequence":3}}

data: {"type":"stream_chunk","id":"evt-123460","created_at":"2023-06-01T12:34:56Z","data":{"content":" equals ","sequence":4}}

data: {"type":"stream_chunk","id":"evt-123461","created_at":"2023-06-01T12:34:56Z","data":{"content":"4","sequence":5}}

data: {"type":"stream_chunk","id":"evt-123462","created_at":"2023-06-01T12:34:56Z","data":{"content":".","sequence":6}}

data: {"type":"stream_end","id":"evt-123463","created_at":"2023-06-01T12:34:56Z","data":{"complete_text":"2+2 equals 4."}}
```

## Example Conversation

1. Create a session:

```bash
curl -X POST http://localhost:8080/sessions
```

2. Send a message to the session:

```bash
curl -X POST http://localhost:8080/sessions/[YOUR_SESSION_ID]/run_stream \
  -H "Content-Type: application/json" \
  -d '{"message":"Hello, I need to perform some calculations."}' \
  -N
```

3. Follow up with a calculation request:

```bash
curl -X POST http://localhost:8080/sessions/[YOUR_SESSION_ID]/run_stream \
  -H "Content-Type: application/json" \
  -d '{"message":"What is 15 * 23?"}' \
  -N
```

4. The AI will remember the context of the conversation and can use the calculator tool to compute the answer. 