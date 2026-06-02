# TencentDB Agent Memory Integration Example

This example demonstrates how to integrate TencentDB Agent Memory as a
sidecar-backed long-term memory engine using the **ingest + recall plugin**
pattern. The Go framework keeps runner/session/tool wiring in-process while
delegating memory capture, indexing, and recall to the TencentDB Agent Memory
gateway.

## Overview

The integration works in three parts:

1. **Session ingestion** — After each conversation turn the Runner sends the
   new session transcript to the gateway via `runner.WithSessionIngestor(...)`.
2. **Automatic recall** — Before each model call, `runner.WithPlugins(...)`
   invokes the TencentDB recall endpoint and injects the returned context into
   the model request. Recall is opt-in (`WithRecallEnabled(true)`).
3. **Read-only tools** — The agent can explicitly search memory through
   `tdai_memory_search` (opt-in via `WithMemorySearchTool(true)`) and
   conversation history through `tdai_conversation_search`.

> **Multi-tenant note:** automatic recall and `tdai_memory_search` read from the
> gateway's shared long-term store, which does not currently enforce
> user/session scoping. They are therefore disabled by default. Only the
> session-scoped capture and `tdai_conversation_search` surfaces are on by
> default. This demo enables recall and memory search explicitly because it runs
> a single trusted local sidecar.

### Architecture

```text
User message
      │
      ▼
   Runner ──► BeforeModel plugin ──► TencentDB Agent Memory gateway
      │              │                         │
      │              ▼                         ▼
      │        recalled context           SDK memory engine
      │
      ▼
   Agent ──► LLM ──► Response
      │        │
      │   (may call tdai_memory_search
      │    or tdai_conversation_search)
      │
      ▼  (after turn completes)
 session.Ingestor ──► /capture
```

### What This Example Does

The program starts an interactive chat loop:

1. Send a few messages that contain stable facts or preferences.
2. Use `/new` to flush the current session and start a fresh session for the
   same user.
3. Ask related questions in the new session. The recall plugin and native
   search tools can retrieve memories extracted by the gateway.

## Prerequisites

- Go 1.21 or later
- A running TencentDB Agent Memory gateway sidecar
- A valid OpenAI-compatible API key for the chat model

The gateway is the local HTTP facade over the TencentDB Agent Memory SDK. It is
responsible for the L0-L3 memory engine: capture, extraction, storage, recall,
and search. If the SDK is configured to use local SQLite, the gateway is still
needed because direct VectorDB storage access does not run the SDK's memory
pipeline.

### Start the Gateway

Clone the TencentDB Agent Memory repository and start the standalone gateway:

```bash
git clone https://github.com/Tencent/TencentDB-Agent-Memory.git
cd TencentDB-Agent-Memory
npm install

export TDAI_LLM_API_KEY="your-openai-compatible-api-key"
export TDAI_LLM_BASE_URL="https://api.openai.com/v1"
export TDAI_LLM_MODEL="deepseek-v4-flash"

node --import tsx src/gateway/server.ts
```

The example connects to `http://127.0.0.1:8420` by default. The gateway reads
the `TDAI_LLM_*` variables above for extraction and recall. You can point the Go
example at another gateway URL with `-gateway`.

## Environment Variables

| Variable                         | Required | Description                         | Default                  |
| -------------------------------- | -------- | ----------------------------------- | ------------------------ |
| `OPENAI_API_KEY`                 | Yes      | API key for the chat model          |                          |
| `OPENAI_BASE_URL`                | No       | Base URL for the model API endpoint | `https://api.openai.com/v1` |
| `TENCENTDB_AGENT_MEMORY_GATEWAY` | No       | TencentDB Agent Memory gateway URL  | `http://127.0.0.1:8420`  |
| `TDAI_GATEWAY_API_KEY`           | No       | Gateway API key (sent as `Authorization: Bearer`) when the gateway requires auth | |

## Command Line Arguments

| Argument             | Description                                      | Default                    |
| -------------------- | ------------------------------------------------ | -------------------------- |
| `-model`             | Chat model name                                  | `deepseek-v4-flash`        |
| `-app`               | Application name used for session ownership      | `tencentdb-memory-demo`    |
| `-user`              | User ID used for session ownership               | `demo-user`                |
| `-session`           | Session ID (auto-generated if empty)             | `tencentdb-<unix-time>`    |
| `-gateway`           | TencentDB Agent Memory gateway URL               | env or `http://127.0.0.1:8420` |
| `-gateway-timeout`   | Timeout for gateway calls, including session flush | `60s`                   |
| `-gateway-api-key`   | Gateway API key sent as `Authorization: Bearer`  | env `TDAI_GATEWAY_API_KEY`  |
| `-turn-wait`         | Delay after each turn for gateway capture/extraction | `0s`                   |
| `-end-session`       | Call `/session/end` before exit                  | `false`                    |

## Usage

### Quick Start

```bash
export OPENAI_API_KEY="your-openai-api-key"
export TENCENTDB_AGENT_MEMORY_GATEWAY="http://127.0.0.1:8420"

cd examples/memory/tencentdb
go run .
```

Then try:

```text
You: Remember this profile: my project code name is Apollo Lake, my deployment window is Friday night, and I prefer concise answers.
You: /new
You: What is my project code name, deployment window, and answer preference?
You: /exit
```

### Custom Model

```bash
go run . -model gpt-4o-mini
```

### Custom Gateway

```bash
go run . -gateway http://127.0.0.1:8420
```

### Expected Output

```text
Model: deepseek-v4-flash
Gateway: http://127.0.0.1:8420 (status=ok version=...)
App: tencentdb-memory-demo
User: demo-user
Session: tencentdb-1713012345
============================================================
Special commands:
  /new      - flush current session and start a new session for the same user
  /session  - show current session
  /end      - call TencentDB Agent Memory /session/end for current session
  /exit     - end the conversation

You: My project code name is Apollo Lake. I prefer concise answers.
Tool calls: tdai_memory_search, tdai_conversation_search
Assistant: Noted.

You: /new
Started new session.

You: What is my project code name?
Tool calls: tdai_memory_search
Assistant: Your project code name is Apollo Lake.
```

## Integration Pattern

The core wiring in Go looks like this:

```go
import (
    memorytencentdb "trpc.group/trpc-go/trpc-agent-go/memory/tencentdb"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// 1. Create the TencentDB Agent Memory service.
//    Recall and the long-term memory_search tool are opt-in; enable them only
//    when the gateway enforces per-user/session isolation (e.g. a trusted local
//    sidecar). Pass WithAPIKey when the gateway sets TDAI_GATEWAY_API_KEY.
memSvc, err := memorytencentdb.NewService(
    memorytencentdb.WithGatewayURL("http://127.0.0.1:8420"),
    memorytencentdb.WithRecallEnabled(true),
    memorytencentdb.WithMemorySearchTool(true),
    // memorytencentdb.WithAPIKey(os.Getenv("TDAI_GATEWAY_API_KEY")),
)
if err != nil {
    log.Fatalf("create memory service: %v", err)
}
defer memSvc.Close()

// 2. Create the agent with TencentDB-native memory tools.
agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("deepseek-v4-flash")),
    llmagent.WithTools(memSvc.Tools()),
)

// 3. Create the runner with ingestion and automatic recall enabled.
r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(memSvc),
    runner.WithPlugins(memSvc.Plugin()),
)
defer r.Close()
```

Key points:

- `memSvc.Tools()` returns `tdai_conversation_search` by default;
  `tdai_memory_search` is added only when `WithMemorySearchTool(true)` is set.
- `runner.WithSessionIngestor(memSvc)` sends timestamped session transcript
  messages to the gateway after each turn.
- `runner.WithPlugins(memSvc.Plugin())` performs automatic recall before model
  calls and injects returned context into the request, but only when
  `WithRecallEnabled(true)` is set.
- The adapter forwards app/user/session identifiers, but hard multi-tenant
  isolation depends on the gateway and SDK honoring those fields end-to-end, so
  cross-session/user reads (recall and memory search) are opt-in.

## Configuration Options

| Option                         | Description                                         | Default                 |
| ------------------------------ | --------------------------------------------------- | ----------------------- |
| `WithGatewayURL(url)`          | TencentDB Agent Memory gateway URL                  | `http://127.0.0.1:8420` |
| `WithTimeout(d)`               | HTTP timeout for gateway requests                   | `5s`                    |
| `WithIngestWorkers(n)`         | Number of async capture workers                     | `1`                     |
| `WithIngestQueueSize(n)`       | Queue size for async capture jobs                   | `10`                    |
| `WithIngestJobTimeout(d)`      | Timeout for queued capture jobs                     | `30s`                   |
| `WithSessionKeyFunc(fn)`       | Custom framework session to gateway `session_key` mapping | app:user:session |
| `WithAPIKey(key)`              | Send `Authorization: Bearer <key>` (gateway `TDAI_GATEWAY_API_KEY`) | none      |
| `WithRecallEnabled(bool)`      | Enable automatic recall plugin behavior (opt-in; shared-store reads) | `false`        |
| `WithMemorySearchTool(bool)`   | Expose `tdai_memory_search` (opt-in; shared-store reads) | `false`              |
| `WithConversationSearchTool(bool)` | Expose `tdai_conversation_search`               | `true`                  |
| `WithStandardAliases(bool)`    | Also expose standard `memory_search` alias (needs memory search enabled) | `false` |
| `WithToolPrefix(prefix)`       | Change native tool prefix                           | `tdai`                  |

## See Also

- [Mem0 Integration Example](../mem0/) — Ingest-first external memory platform
- [Simple Memory Example](../simple/) — Agentic mode with manual tool calling
- [Auto Memory Example](../auto/) — Automatic background extraction using a
  local LLM extractor
- [Memory Documentation](../../../docs/mkdocs/en/memory.md)
- [Ecosystem Integration Guide](../../../docs/mkdocs/en/ecosystem.md)
