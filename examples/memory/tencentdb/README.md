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
   performs TencentDB recall and injects the returned context into the model
   request. Legacy calls `/recall`; V3 composes L1/L2/L3 reads. Recall is opt-in
   (`WithRecallEnabled(true)`).
3. **Read-only tools** — The agent can explicitly search memory through
   `tdai_memory_search` (opt-in via `WithMemorySearchTool(true)`) and
   conversation history through `tdai_conversation_search`. V3 integrations
   also expose `tdai_read_scenario` to read an L2 file selected from scene
   navigation.
4. **Context offload v2 (optional)** — Tool results are sent to
   `/v2/offload/ingest`; model context is compacted through
   `/v2/offload/compact`; and the agent can recover archived details with
   `tdai_read_offload_ref`, backed by `/v2/offload/read-ref`.

> **Multi-tenant note:** automatic recall and `tdai_memory_search` remain
> disabled by default for compatibility with legacy gateways. New cloud and
> self-hosted integrations should use `NewServiceWithIdentity`; the current data
> plane then scopes L0/L1 by service, team, agent, and user. L2/L3 are
> intentionally shared at the team-and-agent level.

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
      │   (may call tdai_memory_search,
      │    tdai_conversation_search, or
      │    tdai_read_scenario)
      │
      ▼  (after turn completes)
 session.Ingestor ──► Legacy /capture or V3 /v3/conversation/add
```

### What This Example Does

The program starts an interactive chat loop:

1. Start the walkthrough with `-turn-wait` and send a message that contains
   stable facts or preferences.
2. Wait until the configured post-turn delay finishes and the next prompt
   appears. This gives the gateway time for asynchronous extraction but is not
   a readiness guarantee.
3. Use `/new` to finish pending local capture work and start a fresh session for
   the same user. Legacy gateways also receive `/session/end`; V3 has no remote
   session-end or extraction-barrier endpoint.
4. Ask related questions in the new session. V3 cross-session recall succeeds
   after the gateway has extracted the previous turn into long-term memory.

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
git clone https://github.com/TencentCloud/TencentDB-Agent-Memory.git
cd TencentDB-Agent-Memory/MemoryCore
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
| `TDAI_GATEWAY_API_KEY`           | For authenticated gateway/offload | Gateway API key sent as `Authorization: Bearer`; V3 uses a non-secret `local` placeholder when omitted for a self-hosted gateway with authentication disabled | |
| `TDAI_SERVICE_ID`                | For identity | Memory service ID sent as `X-TDAI-Service-Id` for V3 | |
| `TDAI_TEAM_ID`                   | For identity | Team isolation ID | |
| `TDAI_AGENT_ID`                  | For identity | Agent isolation ID | |
| `TDAI_OFFLOAD_SERVICE_ID`        | For offload | Service ID for the optional context offload v2 integration | |

`TDAI_SERVICE_ID` now selects the V3 memory identity path. Existing offload
setups that previously reused that variable should move the offload value to
`TDAI_OFFLOAD_SERVICE_ID`; the two integrations may use different service IDs.

## Command Line Arguments

| Argument             | Description                                      | Default                    |
| -------------------- | ------------------------------------------------ | -------------------------- |
| `-model`             | Chat model name                                  | `deepseek-v4-flash`        |
| `-app`               | Application name used for session ownership      | `tencentdb-memory-demo`    |
| `-user`              | User ID used for session ownership               | `demo-user`                |
| `-session`           | Session ID (auto-generated if empty)             | `tencentdb-<unix-time>`    |
| `-gateway`           | TencentDB Agent Memory gateway URL               | env or `http://127.0.0.1:8420` |
| `-gateway-timeout`   | Timeout for gateway calls                          | `60s`                   |
| `-gateway-api-key`   | Optional gateway API key sent as `Authorization: Bearer`; required by context offload v2 | env `TDAI_GATEWAY_API_KEY`  |
| `-service-id`        | Service ID; setting any service/team/agent ID requests the identity-scoped data plane | env `TDAI_SERVICE_ID` |
| `-team-id`           | Team ID; setting any service/team/agent ID requests the identity-scoped data plane | env `TDAI_TEAM_ID` |
| `-agent-id`          | Agent ID; setting any service/team/agent ID requests the identity-scoped data plane | env `TDAI_AGENT_ID` |
| `-offload-service-id` | Service ID for context offload v2; requires `-gateway-api-key` | env `TDAI_OFFLOAD_SERVICE_ID` |
| `-turn-wait`         | Fixed delay after each completed turn to allow asynchronous extraction before cross-session recall; not a readiness guarantee | `0s` |
| `-end-session`       | End before exit: call Legacy `/session/end`, or wait for pending V3 capture | `false` |

## Usage

### Quick Start

```bash
export OPENAI_API_KEY="your-openai-api-key"
export TENCENTDB_AGENT_MEMORY_GATEWAY="http://127.0.0.1:8420"
export TDAI_GATEWAY_API_KEY="your-gateway-api-key"
export TDAI_SERVICE_ID="your-memory-service-id"
export TDAI_TEAM_ID="your-team-id"
export TDAI_AGENT_ID="your-agent-id"

cd examples/memory/tencentdb
go run . -turn-wait 10s
```

Then try the following flow. Wait until the delay finishes and the next `You:`
prompt appears before entering `/new`:

```text
You: Remember this profile: my project code name is Apollo Lake, my deployment window is Friday night, and I prefer concise answers.
Waiting 10s to allow asynchronous gateway extraction...
You: /new
You: What is my project code name, deployment window, and answer preference?
You: /exit
```

The delay is an allowance rather than a server readiness check. Increase it or
verify extraction through gateway observability when the deployment needs more
time. `/new` itself waits only for local capture; it does not wait for V3 L1
extraction to finish.

### Custom Model

```bash
go run . -model gpt-4o-mini
```

### Custom Gateway

```bash
go run . -gateway http://127.0.0.1:8420
```

### Context Offload V2

The v2 routes require both a non-empty Bearer key and service ID. For a
standalone gateway, the upstream convention is `local` and `default`:

```bash
go run . \
  -gateway-api-key local \
  -offload-service-id default
```

For a managed service or an authenticated self-hosted gateway, use the assigned
API key. A self-hosted gateway that keeps authentication disabled can omit
`-gateway-api-key` for V3 memory calls. Context offload v2 still requires a key.
The example never needs direct COS credentials.

### Expected Output

```text
Model: deepseek-v4-flash
Gateway: http://127.0.0.1:8420 (status=ok version=...)
App: tencentdb-memory-demo
User: demo-user
Session: tencentdb-1713012345
============================================================
Special commands:
  /new      - finish pending capture and start a new session for the same user
  /session  - show current session
  /end      - end current session
  /exit     - end the conversation

You: My project code name is Apollo Lake. I prefer concise answers.
Tool calls: tdai_memory_search, tdai_conversation_search
Assistant: Noted.
Waiting 10s to allow asynchronous gateway extraction...

You: /new
Started new session.
  V3 capture is complete; asynchronous long-term extraction may still be running.

You: What is my project code name?
Tool calls: tdai_memory_search
Assistant: Your project code name is Apollo Lake.
```

## Integration Pattern

The core wiring in Go looks like this:

```go
import (
    "os"

    memorytencentdb "trpc.group/trpc-go/trpc-agent-go/memory/tencentdb"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// 1. Create the TencentDB Agent Memory service.
//    New cloud and self-hosted integrations use the same identity-scoped API.
identity := memorytencentdb.NewServiceIdentity(
    os.Getenv("TDAI_SERVICE_ID"),
    os.Getenv("TDAI_TEAM_ID"),
    os.Getenv("TDAI_AGENT_ID"),
)
memSvc, err := memorytencentdb.NewServiceWithIdentity(
    identity,
    memorytencentdb.WithGatewayURL("http://127.0.0.1:8420"),
    // Optional only for a self-hosted gateway with authentication disabled.
    memorytencentdb.WithAPIKey(os.Getenv("TDAI_GATEWAY_API_KEY")),
    memorytencentdb.WithRecallEnabled(true),
    memorytencentdb.WithMemorySearchTool(true),
    // memorytencentdb.WithContextOffload(memorytencentdb.ContextOffloadConfig{
    //     Enabled:   true,
    //     ServiceID: os.Getenv("TDAI_OFFLOAD_SERVICE_ID"),
    // }),
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
    runner.WithPlugins(
        memSvc.Plugin(),
        memSvc.ContextOffloadPlugin(),
    ),
)
defer r.Close()
```

Key points:

- `memSvc.Tools()` returns `tdai_conversation_search` by default;
  `tdai_memory_search` is added only when `WithMemorySearchTool(true)` is set.
- `runner.WithSessionIngestor(memSvc)` sends session transcript messages to the
  gateway after each turn. V3 preserves event timestamps and sends the
  documented conversation-add request in ordered batches of at most 100. It
  truncates individual L0 messages beyond the gateway's 8192-character limit,
  marks them with `...[truncated]`, and continues with later messages. Legacy
  keeps its compatibility timestamps. Capture is at-least-once: the checkpoint
  advances only after the complete capture is acknowledged, so an ambiguous
  transport failure can replay L0 messages.
- `runner.WithPlugins(memSvc.Plugin())` performs automatic recall before model
  calls and injects returned context into the request, but only when
  `WithRecallEnabled(true)` is set.
- `runner.WithPlugins(memSvc.ContextOffloadPlugin())` activates only when
  `ContextOffloadConfig.Enabled` is true. The companion
  `tdai_read_offload_ref` tool is then included in `memSvc.Tools()`.
- `NewServiceWithIdentity` selects the current identity-scoped API. L0/L1
  memory is isolated by service, team, agent, and framework user; L2/L3 memory
  is shared by users of the same team and agent. `NewService` preserves the
  legacy gateway routes. `AppName` and `WithSessionKeyFunc` are not sent as V3
  isolation fields; use distinct service, team, or agent identities when
  applications must not share memory.
- V3 adds `tdai_read_scenario` to `memSvc.Tools()` so the agent can read L2
  content in the configured service/team/agent scope.
- `EndSession` waits for queued capture in both modes. It additionally calls
  `/session/end` only for Legacy gateways because V3 has no remote equivalent.
  It is not a V3 long-term extraction barrier; use an explicit delay or gateway
  readiness signal before demonstrating cross-session recall.

## Configuration Options

Use `NewService` for the Legacy gateway API. For the V3 data plane shared by
cloud and self-hosted gateways, create a `ServiceIdentity` with
`NewServiceIdentity(serviceID, teamID, agentID)`, then pass it to
`NewServiceWithIdentity(identity, opts...)`. All three IDs are required.

| Option                         | Description                                         | Default                 |
| ------------------------------ | --------------------------------------------------- | ----------------------- |
| `WithGatewayURL(url)`          | TencentDB Agent Memory gateway URL                  | `http://127.0.0.1:8420` |
| `WithTimeout(d)`               | HTTP timeout for gateway requests                   | `5s`                    |
| `WithIngestWorkers(n)`         | Number of async capture workers                     | `1`                     |
| `WithIngestQueueSize(n)`       | Queue size for async capture jobs                   | `10`                    |
| `WithIngestJobTimeout(d)`      | Timeout for queued capture jobs                     | `30s`                   |
| `WithSessionKeyFunc(fn)`       | Custom framework session to gateway `session_key` mapping | base64url(app):base64url(user):base64url(session) |
| `WithAPIKey(key)`              | Send `Authorization: Bearer <key>` (gateway `TDAI_GATEWAY_API_KEY`) | none      |
| `WithRecallEnabled(bool)`      | Enable automatic recall; Legacy may read a shared store, while V3 scopes L1 by user and L2/L3 by team/agent | `false` |
| `WithMemorySearchTool(bool)`   | Expose `tdai_memory_search`; Legacy may read a shared store, while V3 scopes L1 by user | `false` |
| `WithConversationSearchTool(bool)` | Expose `tdai_conversation_search`               | `true`                  |
| `WithStandardAliases(bool)`    | Also expose standard `memory_search` alias (needs memory search enabled) | `false` |
| `WithToolPrefix(prefix)`       | Change native tool prefix                           | `tdai`                  |
| `WithContextOffload(config)`   | Configure the opt-in context offload v2 integration | disabled                |

## See Also

- [Mem0 Integration Example](../mem0/) — Ingest-first external memory platform
- [Simple Memory Example](../simple/) — Agentic mode with manual tool calling
- [Auto Memory Example](../auto/) — Automatic background extraction using a
  local LLM extractor
- [Memory Documentation](../../../docs/mkdocs/en/memory/index.md)
- [Ecosystem Integration Guide](../../../docs/mkdocs/en/ecosystem.md)
