# Message Snapshot Example

This example demonstrates how to expose both the regular AG-UI chat endpoint and the message snapshot endpoint so that a client can replay the full conversation history on demand.

- `server/`: Go server that runs an agent, persists events in an in-memory session store, and enables `MessagesSnapshot`.
- `client/`: Minimal TypeScript script that first triggers a chat run and then fetches the snapshot history for the same thread.

## Run the server

From the repository root:

```bash
cd trpc-agent-go/examples/agui/messagessnapshot/server
go run .
```

The server listens on `http://127.0.0.1:8080` by default, exposing:

- Chat endpoint: `http://127.0.0.1:8080/agui`
- Snapshot endpoint: `http://127.0.0.1:8080/history`

You can override these paths using the `-path` and `-messages-snapshot-path` flags.

Output:

```log
2025-11-04T20:44:19+08:00       INFO    server/main.go:83       AG-UI: serving agent "agui-agent" on http://127.0.0.1:8080/agui
2025-11-04T20:44:19+08:00       INFO    server/main.go:84       AG-UI: messages snapshot available at http://127.0.0.1:8080/history
```

## Run the client

Open a new terminal:

```bash
cd trpc-agent-go/examples/agui/messagessnapshot/client
pnpm install
pnpm dev
```

Environment variables recognised by the script:

| Variable | Description | Default |
|----------|-------------|---------|
| `AG_UI_ENDPOINT` | Chat endpoint URL | `http://127.0.0.1:8080/agui` |
| `AG_UI_HISTORY_ENDPOINT` | Snapshot endpoint URL | `http://127.0.0.1:8080/history` |
| `AG_UI_USER_ID` | User identifier forwarded to the server | `demo-user` |
| `AG_UI_PROMPT` | Prompt used for the chat run | Sample math question |
| `AG_UI_THREAD_ID` | Conversation thread ID | Auto-generated timestamp |

Example:

```bash
AG_UI_USER_ID=alice pnpm dev
```

The script first sends the prompt to the chat endpoint and prints the latest responses. It then calls the snapshot endpoint for the same `threadId`/`userId` pair and logs the full message history returned by the server.

Output:

```log

> agui-historysync-client@0.1.0 dev /cbs/workspace/support-agui/trpc-agent-go/examples/agui/messagessnapshot/client
> tsx src/index.ts

Send chat request to -> http://127.0.0.1:8080/agui
Latest AG-UI response:
assistant: I'll help you calculate 2*(10+11) step by step.

First, let's calculate the expression inside the parentheses: 10 + 11
tool(call_00_F8fLH0ufQ8iKvrDFRxMZVWa0): {"result":21}
assistant: Now we have 2 * 21. Let's calculate that:
tool(call_00_zE79N6ho0jL1pmZfwFV7Fxqv): {"result":42}
assistant: **Explanation of the process:**
1. According to the order of operations (PEMDAS/BODMAS), we first calculate what's inside the parentheses: 10 + 11 = 21
2. Then we multiply the result by 2: 2 × 21 = 42

**Final conclusion:**
2*(10+11) = 42
Load history -> http://127.0.0.1:8080/history
History message snapshot:
{
  "id": "cf0144f8-1baf-41c5-b911-02891d9a4713",
  "role": "user",
  "content": "Please help me calculate 2*(10+11), explain the process, then calculate, and give the final conclusion."
}
{
  "id": "eecf0e40-8faa-412f-bb7a-72ce40b23a09",
  "role": "assistant",
  "content": "I'll help you calculate 2*(10+11) step by step.\n\nFirst, let's calculate the expression inside the parentheses: 10 + 11",
  "toolCalls": [
    {
      "id": "call_00_F8fLH0ufQ8iKvrDFRxMZVWa0",
      "type": "function",
      "function": {
        "name": "calculator",
        "arguments": "{\"a\": 10, \"b\": 11, \"operation\": \"add\"}"
      }
    }
  ]
}
{
  "id": "bcb0d913-4759-4e6f-a80a-25acc0a65b8a",
  "content": "{\"result\":21}",
  "role": "tool",
  "toolCallId": "call_00_F8fLH0ufQ8iKvrDFRxMZVWa0"
}
{
  "id": "ec6c80a0-e6dd-4afe-bc28-7bc3b70cd15d",
  "role": "assistant",
  "content": "Now we have 2 * 21. Let's calculate that:",
  "toolCalls": [
    {
      "id": "call_00_zE79N6ho0jL1pmZfwFV7Fxqv",
      "type": "function",
      "function": {
        "name": "calculator",
        "arguments": "{\"a\": 2, \"b\": 21, \"operation\": \"multiply\"}"
      }
    }
  ]
}
{
  "id": "fc4fc26a-55ba-4bed-8a13-5cb301e2dd68",
  "content": "{\"result\":42}",
  "role": "tool",
  "toolCallId": "call_00_zE79N6ho0jL1pmZfwFV7Fxqv"
}
{
  "id": "34f69eef-8f70-4449-b0a6-e857fa8d9349",
  "role": "assistant",
  "content": "**Explanation of the process:**\n1. According to the order of operations (PEMDAS/BODMAS), we first calculate what's inside the parentheses: 10 + 11 = 21\n2. Then we multiply the result by 2: 2 × 21 = 42\n\n**Final conclusion:**\n2*(10+11) = 42"
}
threadId=thread-1762260377788, userId=demo-user
```
