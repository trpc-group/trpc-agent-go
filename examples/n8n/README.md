# n8n Agent Examples

Examples demonstrating how to use the n8n agent with trpc-agent-go.

## Prerequisites

- A running n8n instance (self-hosted or cloud)
- A webhook workflow configured in n8n
- Go 1.21 or later

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `N8N_WEBHOOK_URL` | Yes | The webhook URL from your n8n workflow |
| `N8N_API_KEY` | No | API key for header authentication (sent as `Authorization: Bearer <key>`) |

## Running the Examples

### Basic Chat (Non-Streaming)

```bash
cd examples/n8n
export N8N_WEBHOOK_URL="https://your-n8n-instance.com/webhook/your-webhook-id"
go run ./basic_chat/
```

### Streaming Chat

```bash
cd examples/n8n
export N8N_WEBHOOK_URL="https://your-n8n-instance.com/webhook/your-webhook-id"
go run ./streaming_chat/
```

## n8n Workflow Setup

### Basic (Non-Streaming) Workflow

1. Create a new workflow in n8n.
2. Add a **Webhook** node as the trigger.
   - Set HTTP Method to `POST`.
   - Copy the webhook URL for use as `N8N_WEBHOOK_URL`.
3. Connect your processing nodes (e.g., AI Agent, HTTP Request, Code nodes).
4. End with a **Respond to Webhook** node that returns JSON with an `output` field:
   ```json
   { "output": "The assistant's response text" }
   ```
5. Activate the workflow.

### Streaming Workflow

1. Create a new workflow in n8n.
2. Add a **Webhook** node as the trigger.
   - Set HTTP Method to `POST`.
3. Connect your processing nodes.
4. End with a **Respond to Webhook** node configured to return Server-Sent Events (SSE).
   - Each SSE chunk should follow the format: `data: {"output": "chunk text"}\n\n`
   - Send `data: [DONE]\n\n` to signal the end of the stream.
5. Activate the workflow.

### Authentication

If your n8n webhook requires authentication, set the `N8N_API_KEY` environment variable. The examples will automatically include it as a `Bearer` token in the `Authorization` header.

For basic authentication or custom header names, modify the agent options in the example code:

```go
// Basic auth
n8n.WithAuthType(n8n.AuthBasic),
n8n.WithAuthConfig(&n8n.AuthConfig{
    Username: "user",
    Password: "pass",
}),

// Custom header
n8n.WithAuthType(n8n.AuthHeader),
n8n.WithAuthConfig(&n8n.AuthConfig{
    HeaderName:  "X-Custom-Auth",
    HeaderValue: "your-token",
}),
```
