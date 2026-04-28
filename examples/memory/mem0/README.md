# Mem0 Integration Example

This example demonstrates how to integrate [mem0](https://mem0.ai) as an
external long-term memory platform using the **ingest-first** pattern. Unlike
the built-in memory backends (`simple/`, `auto/`), this approach delegates
memory extraction entirely to the mem0 service and exposes only read-only
tools to the agent.

## Overview

The integration works in two parts:

1. **Session ingestion** — After each conversation turn the Runner sends the
   new session transcript to mem0 via `runner.WithSessionIngestor(...)`. Mem0
   analyses the raw messages and decides what to remember on its own.
2. **Read-only tools** — The agent can search and load memories through
   `memory_search` and (optionally) `memory_load`. Write tools such as
   `memory_add` / `memory_update` / `memory_delete` are intentionally not
   exposed because mem0 handles the write side natively.

### Architecture

```text
User message
      │
      ▼
   Runner  ──►  Agent  ──►  LLM  ──►  Response
      │                        │
      │                  (may call memory_search
      │                   or memory_load tools)
      │
      ▼  (after turn completes)
  session.Ingestor
      │
      ▼
  mem0 API  ──►  extracts & stores memories
```

### What This Example Does

The program sends a single message to the agent containing a unique token
(e.g. a made-up dog name), then polls mem0 until the ingested memory becomes
searchable. This verifies the full round-trip: agent response → session
ingest → mem0 extraction → memory retrieval.

## Prerequisites

- Go 1.21 or later
- A valid [mem0 API key](https://app.mem0.ai/)
- A valid OpenAI-compatible API key (for the chat model)

## Environment Variables

| Variable          | Required | Description                            | Default                 |
| ----------------- | -------- | -------------------------------------- | ----------------------- |
| `MEM0_API_KEY`    | Yes      | API key for mem0                       |                         |
| `OPENAI_API_KEY`  | Yes      | API key for the chat model             |                         |
| `MEM0_HOST`       | No       | mem0 API base URL                      | `https://api.mem0.ai`   |
| `MEM0_BASE_URL`   | No       | Alias for `MEM0_HOST`                  |                         |
| `MEM0_ORG_ID`     | No       | mem0 organization ID                   |                         |
| `MEM0_PROJECT_ID` | No       | mem0 project ID                        |                         |
| `OPENAI_BASE_URL` | No       | Base URL for the model API endpoint    | `https://api.openai.com/v1` |

## Command Line Arguments

| Argument         | Description                                       | Default                    |
| ---------------- | ------------------------------------------------- | -------------------------- |
| `-model`         | Chat model name                                   | `deepseek-v4-flash`            |
| `-app`           | Application name used for mem0 ownership scoping  | `mem0-integration-demo`    |
| `-user`          | User ID used for mem0 ownership scoping           | `demo-user`                |
| `-session`       | Session ID (auto-generated if empty)              | `mem0-<unix-timestamp>`    |
| `-wait-timeout`  | How long to wait for the memory to be readable    | `90s`                      |

## Usage

### Quick Start

```bash
export MEM0_API_KEY="your-mem0-api-key"
export OPENAI_API_KEY="your-openai-api-key"

cd examples/memory/mem0
go run .
```

### Custom Model

```bash
go run . -model gpt-4o-mini
```

### Self-hosted mem0

```bash
export MEM0_HOST="https://your-mem0-instance.example.com"
go run .
```

### Expected Output

```
Model: deepseek-v4-flash
App: mem0-integration-demo
User: demo-user
Session: mem0-1713012345
Token: Mem0IntegrationDemo-1713012345678901234
Message: For future reference, my dog is named Mem0IntegrationDemo-...
============================================================
Tool calls: memory_search
Assistant: Got it! I'll remember your dog's name...

Stored memories (1):
  1. User's dog is named Mem0IntegrationDemo-...
```

## Integration Pattern

The core wiring in Go looks like this:

```go
import (
    memorymem0 "trpc.group/trpc-go/trpc-agent-go/memory/mem0"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

// 1. Create the mem0 service.
mem0Svc, err := memorymem0.NewService(
    memorymem0.WithAPIKey(os.Getenv("MEM0_API_KEY")),
)
if err != nil {
    log.Fatalf("create mem0 service: %v", err)
}
defer mem0Svc.Close()

// 2. Create the agent with read-only memory tools.
agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("deepseek-v4-flash")),
    llmagent.WithTools(mem0Svc.Tools()),
)

// 3. Create the runner with session ingestion enabled.
r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(mem0Svc), // session.Ingestor
)
defer r.Close()
```

Key points:

- `mem0Svc.Tools()` returns `memory_search` and optionally `memory_load`
  (enable with `memorymem0.WithLoadToolEnabled(true)`).
- `runner.WithSessionIngestor(mem0Svc)` hooks the mem0 service into the
  runner's post-turn lifecycle via the `session.Ingestor` interface.
- Mem0 performs its own extraction with `infer: true` — no local LLM
  extractor is needed.

### Per-Request Ingestion Options

`session.Ingestor` accepts variadic `session.IngestOption` values so callers
can attach platform-agnostic per-request settings without breaking the
interface. The runner threads two natural defaults on every turn:

- `session.WithIngestRunID(sess.ID)` → mem0 `run_id`
- `session.WithIngestAgentID(invocation.AgentName)` (falls back to the
  runner's default agent name) → mem0 `agent_id`

Custom callers (or custom Ingestor wrappers) can supply additional metadata:

```go
err := mem0Svc.IngestSession(ctx, sess,
    session.WithIngestMetadata(map[string]any{"channel": "support"}),
    session.WithIngestAgentID("billing-bot"),
    session.WithIngestRunID("ticket-42"),
)
```

mem0 stores the resolved values as `metadata`, `agent_id` and `run_id` on
the underlying memories, which then become available for downstream filtering
and grouping.

## Configuration Options

The mem0 service accepts the following options:

| Option                    | Description                                    | Default                |
| ------------------------- | ---------------------------------------------- | ---------------------- |
| `WithAPIKey(key)`         | mem0 API key                                   | (required)             |
| `WithHost(url)`           | mem0 API base URL                              | `https://api.mem0.ai`  |
| `WithOrgProject(o, p)`   | Organization and project IDs                   |                        |
| `WithAsyncMode(bool)`    | Send ingest requests in async mode             | `true`                 |
| `WithVersion(v)`         | mem0 ingest API version                        | `v2`                   |
| `WithTimeout(d)`         | HTTP timeout for mem0 requests                 | `10s`                  |
| `WithLoadToolEnabled(b)` | Expose `memory_load` in `Tools()`              | `false`                |
| `WithAsyncMemoryNum(n)`  | Number of background ingest workers            | `1`                    |
| `WithMemoryQueueSize(n)` | Queue size for async ingest jobs               | `10`                   |
| `WithMemoryJobTimeout(d)`| Timeout for synchronous ingest fallback        | `30s`                  |

## See Also

- [Simple Memory Example](../simple/) — Agentic mode with manual tool calling
- [Auto Memory Example](../auto/) — Automatic background extraction using a
  local LLM extractor
- [Memory Documentation](../../../docs/mkdocs/en/memory.md)
- [Ecosystem Integration Guide](../../../docs/mkdocs/en/ecosystem.md)
