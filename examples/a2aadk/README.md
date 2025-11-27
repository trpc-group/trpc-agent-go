# A2A ↔ ADK Interop Example

This example shows how the `trpc-agent-go` A2A client talks to an ADK Python A2A server.

- `trpc_agent/main.go`: connects to the ADK server via `a2aagent` and runs three sample prompts (two chats + one tool call).
- `adk/adk_server.py`: builds an ADK A2A server with streaming + tools and logs user/session metadata.

## Directory layout

```
examples/a2aadk/
├── README.md          # This guide
├── trpc_agent/
│   └── main.go        # Go client that calls the ADK server
└── adk/
    └── adk_server.py  # ADK A2A server implementation
```

## Prerequisites

| Component | Version | Notes |
| --------- | ------- | ----- |
| Go        | 1.21+   | Needed for `trpc_agent/main.go` |
| Python    | 3.10+ (3.11 recommended) | Required by the ADK server |
| Pip deps  | See `adk/requirements.txt` | Install via `pip install -r requirements.txt` |
| API key   | `OPENAI_API_KEY` | Example server defaults to OpenAI-compatible models |

## Prepare the ADK/Python side

1. (Optional) Create a virtualenv:
   ```bash
   cd trpc-agent-go/examples/a2aadk/adk
   python3 -m venv venv
   source venv/bin/activate
   ```
2. Install dependencies:
   ```bash
   pip install -r requirements.txt
   ```
3. Configure model access:
   ```bash
   export OPENAI_API_KEY="<your-openai-key>"
   # Optional overrides
   export MODEL_NAME="gpt-4o-mini"
   export OPENAI_API_URL="https://your-endpoint/v1"
   ```

## Start the ADK A2A server

```bash
cd trpc-agent-go/examples/a2aadk/adk
python3 adk_server.py
# Or: uvicorn adk_server:a2a_app --host 0.0.0.0 --port 8081
```

Server highlights:

- Streaming responses plus two example tools: `calculator` and `current_time`.
- `logging_request_converter` wraps `convert_a2a_request_to_agent_run_request` so each request prints `user_id` / `session_id` for debugging.
- You can plug custom `request_converter` / auth modules into `A2aAgentExecutorConfig` to read `X-User-ID` headers and populate ADK sessions.

## Run the Go A2A client

```bash
cd trpc-agent-go
go run ./examples/a2aadk/trpc_agent --url http://localhost:8081
```

Client behavior:

1. Sends three prompts in order (two normal chats plus one tool-using prompt).
2. Streams tool-call/tool-response logs in real time but prints the assistant answer only once at the end.
3. Automatically forwards local `userID` / `sessionID` through the `X-User-ID` HTTP header.
4. Works around ADK's final-event content duplication by capturing content from intermediate events only (see "ADK-specific notes" below).

## ADK-specific notes

| Scenario | Details |
| -------- | ------- |
| No incremental text | ADK A2A events send "full content so far" in every delta (cumulative streaming). The client captures the last valid intermediate event rather than reading deltas incrementally. |
| Final event duplication bug | **Important**: ADK's current A2A implementation may send a malformed final event that duplicates content or prepends the user's question. The client works around this by only capturing content from non-final events and ignoring the final event payload. |
| User propagation | `a2aagent` places `Session.UserID` into the `X-User-ID` header. Override via `a2aagent.WithUserIDHeader(...)` if another header is needed. |
| Reading user ID in ADK | Enable A2A auth/request converter components to extract the header or `AgentRunRequest.user_id` and store it in the session context. `logging_request_converter` in this example is a reference implementation; production setups can inject their own logic via `A2aAgentExecutorConfig`. |
| Session diagnostics | Expect logs like `📋 Session Info - User: xxx, Session: xxx`. They confirm that the header propagated correctly without extra wiring. |

## FAQ

1. **"OPENAI_API_KEY not set" warning**: the server cannot call OpenAI without the env var. Run `export OPENAI_API_KEY=...` before starting `adk_server.py`.
2. **Go client cannot connect**: ensure the ADK server is listening on the `--url` you pass (default `http://localhost:8081`). Adjust either the server port or the flag.
3. **Need to disable ADK compatibility?** This example assumes an ADK peer, so trpc-agent-go keeps the `adk_` metadata prefix enabled. If you target a non-ADK service you can call `a2a.WithADKCompatibility(false)` in your own server, but no change is required here.

After completing the steps above, the Go client and ADK server will interoperate and you can observe tool invocations plus final answers directly in the terminal. Happy debugging!
