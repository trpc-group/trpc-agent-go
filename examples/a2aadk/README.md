# A2A ↔ ADK Interop Example

This example shows how the `trpc-agent-go` A2A client talks to an ADK Python A2A server.

Two example cases are provided:

| Case | Server | Client | Features |
| ---- | ------ | ------ | -------- |
| Tool Call | `adk/adk_server.py` (port 8081) | `trpc_agent/main.go` | Streaming + tools (`calculator`, `current_time`) |
| Code Execution | `adk/adk_codeexec_server.py` (port 8082) | `trpc_agent_codeexec/main.go` | Streaming + Python code execution |

## Directory layout

```
examples/a2aadk/
├── README.md                       # This guide
├── trpc_agent/
│   └── main.go                     # Go client for tool call example
├── trpc_agent_codeexec/
│   └── main.go                     # Go client for code execution example
└── adk/
    ├── adk_server.py               # ADK server with tools
    └── adk_codeexec_server.py      # ADK server with code execution
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

## Case 1: Tool Call Example

### Start the server

```bash
cd trpc-agent-go/examples/a2aadk/adk
python3 adk_server.py
```

### Run the client

```bash
cd trpc-agent-go
go run ./examples/a2aadk/trpc_agent --url http://localhost:8081
```

Server highlights:
- Streaming responses plus two example tools: `calculator` and `current_time`.
- `logging_request_converter` prints `user_id` / `session_id` for debugging.

## Case 2: Code Execution Example

### Start the server

```bash
cd trpc-agent-go/examples/a2aadk/adk
python3 adk_codeexec_server.py
```

### Run the client

```bash
cd trpc-agent-go
go run ./examples/a2aadk/trpc_agent_codeexec --url http://localhost:8082
```

Server highlights:
- ADK agent with `PythonCodeExecution` enabled.
- LLM can generate and execute Python code, returning results via A2A protocol.

Client behavior:
- Displays code execution events (`💻 Code Execution`) and results (`📊 Execution Result`) separately.
- Uses MessageID-based deduplication to handle ADK's repeated event delivery.

## ADK-specific notes

| Scenario | Details |
| -------- | ------- |
| Cumulative streaming | ADK A2A events send "full content so far" in every delta. The client captures the last valid intermediate event rather than reading deltas incrementally. |
| Final event duplication | ADK may send a malformed final event that duplicates content. The client ignores the final event payload. |
| Code execution event duplication | ADK may send repeated code execution events with the same MessageID. The client uses MessageID-based deduplication. |
| User propagation | `a2aagent` places `Session.UserID` into the `X-User-ID` header. Override via `a2aagent.WithUserIDHeader(...)`. |

## FAQ

1. **"OPENAI_API_KEY not set" warning**: the server cannot call OpenAI without the env var. Run `export OPENAI_API_KEY=...` before starting `adk_server.py`.
2. **Go client cannot connect**: ensure the ADK server is listening on the `--url` you pass (default `http://localhost:8081`). Adjust either the server port or the flag.
3. **Need to disable ADK compatibility?** This example assumes an ADK peer, so trpc-agent-go keeps the `adk_` metadata prefix enabled. If you target a non-ADK service you can call `a2a.WithADKCompatibility(false)` in your own server, but no change is required here.

After completing the steps above, the Go client and ADK server will interoperate and you can observe tool invocations plus final answers directly in the terminal. Happy debugging!
