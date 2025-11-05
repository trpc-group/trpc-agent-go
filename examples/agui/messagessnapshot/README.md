# Message Snapshot Example

This example demonstrates how to expose both the regular AG-UI chat endpoint and the message snapshot endpoint so that a client can replay the full conversation history on demand.

- `server/`: Go server that runs an agent, persists events in an in-memory session store, and enables `MessagesSnapshot`.
- `client/`: Minimal TypeScript script that first triggers a chat run and then fetches the snapshot history for the same thread.

The format of AG-UI MessagesSnapshotEvent can be found at [messages](https://docs.ag-ui.com/concepts/messages).

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
2025-11-05T19:39:53+08:00     INFO     server/main.go:83     AG-UI: serving agent "agui-agent" on http://127.0.0.1:8080/agui
2025-11-05T19:39:53+08:00     INFO     server/main.go:84     AG-UI: messages snapshot available at http://127.0.0.1:8080/history
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
⚙️ Send chat request to -> http://127.0.0.1:8080/agui
🤖 assistant: I'll help you calculate 2*(10+11) step by step.

First, let's calculate what's inside the parentheses: 10 + 11
🛠️ tool(call_00_6n7vRxRDjtKl0JiAGpHUUw5E): {"result":21}
🤖 assistant: Now we have 2 * 21. Let's calculate that:
🛠️ tool(call_00_z2GLz0y5qf4X0BqRifgN51Pk): {"result":42}
🤖 assistant: **Process explanation:**
1. First, we follow the order of operations (PEMDAS/BODMAS) which tells us to calculate what's inside parentheses first
2. We calculated 10 + 11 = 21
3. Then we multiplied 2 × 21 = 42

**Final conclusion:** 2*(10+11) = **42**
⚙️ Load history -> http://127.0.0.1:8080/history
👤 user(demo-user): Please help me calculate 2*(10+11), explain the process, then calculate, and give the final conclusion.
🤖 assistant: I'll help you calculate 2*(10+11) step by step.

First, let's calculate what's inside the parentheses: 10 + 11
🛠️ tool(call_00_6n7vRxRDjtKl0JiAGpHUUw5E): {"result":21}
🤖 assistant: Now we have 2 * 21. Let's calculate that:
🛠️ tool(call_00_z2GLz0y5qf4X0BqRifgN51Pk): {"result":42}
🤖 assistant: **Process explanation:**
1. First, we follow the order of operations (PEMDAS/BODMAS) which tells us to calculate what's inside parentheses first
2. We calculated 10 + 11 = 21
3. Then we multiplied 2 × 21 = 42

**Final conclusion:** 2*(10+11) = **42**
threadId=thread-1762342798902, userId=demo-user
```
