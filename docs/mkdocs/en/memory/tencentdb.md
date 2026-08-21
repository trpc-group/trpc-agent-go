# TencentDB Agent Memory Integration (`memory/tencentdb`)

`memory/tencentdb` integrates
[TencentDB Agent Memory](https://github.com/TencentCloud/TencentDB-Agent-Memory)
through its gateway. It is suitable when you want the TencentDB Agent Memory
SDK to own the L0-L3 memory pipeline while tRPC-Agent-Go keeps the Runner,
session, plugin, and tool lifecycle in Go.

The boundary is intentionally different from built-in backends:

- The TencentDB Agent Memory gateway performs capture, extraction, storage,
  recall, and search.
- The Go adapter sends completed session turns through `session.Ingestor`.
- A Runner plugin performs recall before each model request and injects the
  returned context (opt-in via `WithRecallEnabled(true)`). Legacy mode calls
  `/recall`; V3 composes L1 atomic search, L2 scene navigation, and L3 core
  reads.
- Native tools expose read-oriented search through `tdai_conversation_search`
  (session-scoped, on by default) and `tdai_memory_search` (opt-in via
  `WithMemorySearchTool(true)`). V3 integrations also expose
  `tdai_read_scenario` to read bounded L2 content selected from scene
  navigation.
- An optional short-term context offload plugin delegates tool-result
  externalization, L1/L1.5/L2/L3 processing, drill-down, and persistence to
  TencentDB Agent Memory gateway hook APIs. The Go adapter does not write local
  offload files. It is separate from recall and is off by default.

> **Multi-tenant note:** Legacy automatic recall and `tdai_memory_search` can
> read a shared long-term store without user/session scoping. V3 scopes L0/L1
> by service, team, agent, and user, while L2/L3 remain shared across users and
> sessions of the same service, team, and agent. Recall and memory search remain
> disabled by default to preserve existing behavior. `AppName` and
> `WithSessionKeyFunc` are not V3 isolation fields; deployments that must keep
> applications separate should assign distinct service, team, or agent
> identities.

Even when the SDK uses local SQLite storage, the gateway is still required
because it hosts the memory engine. Direct VectorDB or SQLite access only talks
to storage and does not run the SDK's extraction and recall pipeline.

**Use case**: Gateway memory engine, cloud or self-managed storage, automatic
recall before model calls, and external SDK-owned memory extraction.

## Start the TencentDB Agent Memory Gateway

The [upstream package](https://github.com/TencentCloud/TencentDB-Agent-Memory/blob/feat/server_team/MemoryCore/package.json)
requires Node.js 22.16.0 or later. Clone the SDK repository and start the
standalone gateway:

```bash
git clone https://github.com/TencentCloud/TencentDB-Agent-Memory.git
cd TencentDB-Agent-Memory/MemoryCore
npm install

export TDAI_LLM_API_KEY="your-openai-compatible-api-key"
export TDAI_LLM_BASE_URL="https://api.openai.com/v1"
export TDAI_LLM_MODEL="deepseek-v4-flash"

node --import tsx src/gateway/server.ts
```

The gateway listens on `http://127.0.0.1:8420` by default. It reads
`TDAI_LLM_API_KEY`, `TDAI_LLM_BASE_URL`, and `TDAI_LLM_MODEL` for extraction,
scene/persona generation, and recall.

## Configuration Example

```go
import (
    "os"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    memorytencentdb "trpc.group/trpc-go/trpc-agent-go/memory/tencentdb"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

gatewayURL := os.Getenv("TENCENTDB_AGENT_MEMORY_GATEWAY")
if gatewayURL == "" {
    gatewayURL = "http://127.0.0.1:8420"
}

identity := memorytencentdb.NewServiceIdentity(
    os.Getenv("TDAI_SERVICE_ID"),
    os.Getenv("TDAI_TEAM_ID"),
    os.Getenv("TDAI_AGENT_ID"),
)
memSvc, err := memorytencentdb.NewServiceWithIdentity(
    identity,
    memorytencentdb.WithGatewayURL(gatewayURL),
    // Recommended for new cloud and self-hosted integrations. This selects the
    // identity-scoped data plane; all IDs are required. The API key is required
    // only when the gateway enables shared-secret authentication. Without one,
    // the adapter sends the non-secret Bearer placeholder expected by the
    // self-hosted gateway parser.
    // memorytencentdb.WithAPIKey(os.Getenv("TDAI_GATEWAY_API_KEY")),
    // Recall/search remain opt-in.
    memorytencentdb.WithRecallEnabled(true),
    memorytencentdb.WithMemorySearchTool(true),
    // Optional short-term context offload through the gateway v2 API.
    // memorytencentdb.WithContextOffload(memorytencentdb.ContextOffloadConfig{
    //     Enabled:   true,
    //     ServiceID: os.Getenv("TDAI_OFFLOAD_SERVICE_ID"),
    // }),
)
if err != nil {
    panic(err)
}
defer memSvc.Close()

sessionSvc := sessioninmemory.NewSessionService()
agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("deepseek-v4-flash")),
    llmagent.WithTools(memSvc.Tools()),
)

r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(memSvc),
    runner.WithPlugins(
        memSvc.Plugin(),
        // memSvc.ContextOffloadPlugin(),
    ),
)
defer r.Close()
```

**Integration points**:

- Register TencentDB-native search tools with `llmagent.WithTools(memSvc.Tools())`
- Use `runner.WithSessionIngestor(memSvc)` to send session transcripts to
  Legacy `/capture` or V3 `/v3/conversation/add`; V3 preserves event
  timestamps and sends ordered batches of at most 100 messages.
- Use `runner.WithPlugins(memSvc.Plugin())` to enable automatic recall before
  model calls; Legacy calls `/recall`, while V3 composes L1/L2/L3 reads
- Use `runner.WithPlugins(memSvc.ContextOffloadPlugin())` only when
  `WithContextOffload(...)` is enabled and you want short-term context
  offload. The companion `tdai_read_offload_ref` tool is exposed through
  `memSvc.Tools()` when enabled.
- Do **not** use `runner.WithMemoryService(...)` with this integration

## Enable Context Offload

Context offload is a separate, opt-in integration with TencentDB Agent Memory's
v2 offload API. It sends tool results to the gateway for asynchronous
processing, asks the gateway to compact model context when the configured
utilization threshold is reached, and exposes one tool for bounded recovery of
archived results.

Minimal setup:

```go
memSvc, err := memorytencentdb.NewService(
    memorytencentdb.WithGatewayURL(gatewayURL),
    memorytencentdb.WithAPIKey(os.Getenv("TDAI_GATEWAY_API_KEY")),
    memorytencentdb.WithContextOffload(memorytencentdb.ContextOffloadConfig{
        Enabled:   true,
        ServiceID: os.Getenv("TDAI_OFFLOAD_SERVICE_ID"),
    }),
)
if err != nil {
    panic(err)
}

agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("deepseek-v4-flash")),
    llmagent.WithTools(memSvc.Tools()),
)

r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(memSvc),
    runner.WithPlugins(memSvc.ContextOffloadPlugin()),
)
```

The v2 API requires both `Authorization: Bearer <key>` and
`X-TDAI-Service-Id`. For the standalone upstream gateway, use the conventional
values `local` and `default`; for a managed service, use the credentials
assigned to the memory instance.

If offload traffic should use a different gateway or API key from normal
capture/search/recall traffic, override them on `ContextOffloadConfig`:

```go
memorytencentdb.WithContextOffload(memorytencentdb.ContextOffloadConfig{
    Enabled:    true,
    GatewayURL: offloadGatewayURL,
    APIKey:     os.Getenv("TDAI_OFFLOAD_API_KEY"),
    ServiceID:  os.Getenv("TDAI_OFFLOAD_SERVICE_ID"),
})
```

At runtime:

- After tool execution, `ContextOffloadPlugin()` sends real tool call/result
  pairs to `POST /v2/offload/ingest`, together with the latest user prompt and
  bounded recent conversation context. This does not rewrite the current tool
  result message.
- Before each model call, the plugin sends an ingest with no tool pairs for
  L1.5 task judgment. That request still includes the latest user prompt and
  bounded recent conversation context. The plugin then estimates message
  tokens and calls
  `POST /v2/offload/compact` after `CompactionRatio` is reached. Failures are
  best effort: the original model context is retained.
- Alongside the other enabled search tools, `memSvc.Tools()` adds the
  offload-specific `tdai_read_offload_ref`, backed by
  `POST /v2/offload/read-ref`. It supports full, query-centered, or line-range
  recovery, bounded by `max_tokens`.
- The adapter deliberately limits its integration to the three routes needed
  by this lifecycle. Storage, summaries, task maps, and offload policy remain
  gateway responsibilities.

Both ingest paths can send up to 10 recent user or assistant messages after
filtering, truncated to 400 Unicode code points each. The latest user prompt is
sent separately and truncated to 500 code points. Tool messages, assistant
tool-call messages, the duplicated current prompt, and recognized internal
control messages are omitted from `recent_messages`.

The compaction trigger is evaluated locally. The context window is resolved in
this order: `agent.WithModelContextWindow(...)` for the current run, the
model's `Info().ContextWindow` (which providers can expose through options such
as `WithContextWindow(...)`), a value registered for the model name through
`model.RegisterModelContextWindow(...)`, and finally 128,000 tokens.
`TokenCounter` supplies the per-message counts used for both the local
`CompactionRatio` decision and compact request metadata. If it is nil, or if a
custom counter fails or returns a negative value, the plugin uses the simple
token estimator for that model call.

## Interactive Example

Run the example after the gateway is ready:

```bash
cd examples/memory/tencentdb
export OPENAI_API_KEY="your-openai-compatible-api-key"
export TENCENTDB_AGENT_MEMORY_GATEWAY="http://127.0.0.1:8420"
go run . -turn-wait 10s
```

Then send facts and wait until the next `You:` prompt appears before entering
`/new`. The configured delay runs after the completed turn, before the example
accepts the next command:

```text
You: Remember this profile: my project code name is Apollo Lake, my deployment window is Friday night, and I prefer concise answers.
Waiting 10s to allow asynchronous gateway extraction...
You: /new
You: What is my project code name, deployment window, and answer preference?
```

`-turn-wait` is a fixed allowance for the gateway's asynchronous long-term
extraction, not a readiness guarantee. Increase it or verify extraction through
gateway observability if the deployment needs more time. The `/new` command
only waits for pending local capture before switching sessions. Legacy gateways
also receive `/session/end`; V3 has no remote session-end or extraction-barrier
endpoint. V3 conversation search is scoped to the new session, so cross-session
recall depends on the previous turn having reached extracted long-term memory.

## Configuration Options

Use `NewService` for the Legacy gateway API. For the V3 data plane shared by
cloud and self-hosted deployments, create a `ServiceIdentity` with
`NewServiceIdentity(serviceID, teamID, agentID)`, then pass it to
`NewServiceWithIdentity(identity, opts...)`. All three IDs are required.

| Option | Purpose | Default |
| ------ | ------- | ------- |
| `WithGatewayURL(url)` | TencentDB Agent Memory gateway URL. | `http://127.0.0.1:8420` |
| `WithTimeout(d)` | HTTP timeout used by the gateway client. | `5s` |
| `WithIngestWorkers(n)` | Number of async capture workers. | `1` |
| `WithIngestQueueSize(n)` | Queue size for async capture jobs. | `10` |
| `WithIngestJobTimeout(d)` | Timeout for queued capture jobs. | `30s` |
| `WithSessionKeyFunc(fn)` | Customize session to gateway `session_key` mapping. | `base64url(app):base64url(user):base64url(session)` |
| `WithAPIKey(key)` | Send `Authorization: Bearer <key>` (gateway `TDAI_GATEWAY_API_KEY`). | none |
| `WithRecallEnabled(bool)` | Enable automatic recall. Legacy may read a shared store; V3 scopes L1 by user and L2/L3 by team/agent. | `false` |
| `WithMemorySearchTool(bool)` | Expose `tdai_memory_search`. Legacy may read a shared store; V3 scopes L1 by user. | `false` |
| `WithConversationSearchTool(bool)` | Expose `tdai_conversation_search`. | `true` |
| `WithStandardAliases(bool)` | Also expose standard `memory_search` alias (requires memory search enabled). | `false` |
| `WithToolPrefix(prefix)` | Change native tool prefix. | `tdai` |
| `WithContextOffload(ContextOffloadConfig)` | Configure explicit short-term context offload for large tool results. | disabled |

`NewServiceWithIdentity` is the recommended entry point for new integrations.
The opaque `ServiceIdentity` keeps V3 identity configuration separate from the
Legacy-compatible `Options` structure. The V3 client sends `service_id` as
`X-TDAI-Service-Id`, derives `user_id` and `session_id` from the current
framework session, and uses the identity-scoped data-plane routes. `NewService`
preserves the Legacy `/capture`, `/recall`, and `/search/*` behavior. The
constructor describes API semantics rather than deployment type, so cloud and
self-hosted gateways use the same configuration surface. L0/L1 are scoped by
service, team, agent, and user; L2/L3 are shared by users and sessions of the
same service, team, and agent. The optional TencentDB `task_id` is not sent by
this adapter. Self-hosted gateways that keep authentication disabled can omit
`WithAPIKey`; authenticated self-hosted gateways and managed services must
provide it.

When V3 is selected, `memSvc.Tools()` includes `tdai_read_scenario` so the
agent can read an L2 file selected from scene navigation. The complete L1/L2/L3
automatic recall payload is capped at 24 KiB; its L1 atomic and L3 core sections
are each capped at 8 KiB. Recall injects at most 100 non-empty scenario paths
and 8 KiB of navigation text. A shortened recall section places
`...[truncated]` immediately before its closing tag. Scenario reads return at
most 16 KiB; a shortened result ends with `...[truncated]` and sets `truncated`
to `true`.

`ContextOffloadConfig` only controls the Go adapter's gateway integration.
Offload layers, state, storage, TTL, and isolation are owned by the TencentDB
Agent Memory gateway. The Go adapter does not expose local/backend offload
modes and does not write local offload state.

| Field | Purpose | Default |
| ----- | ------- | ------- |
| `Enabled` | Enable the context offload plugin and result-reference tool. | `false` |
| `GatewayURL` | Optional gateway URL override for v2 offload calls. Empty reuses `WithGatewayURL`. | none |
| `APIKey` | Optional Bearer key override for v2 offload calls. Empty reuses `WithAPIKey`. | none |
| `ServiceID` | Memory service ID sent as `X-TDAI-Service-Id`; required when enabled. | none |
| `CompactionRatio` | Context-window utilization that triggers `/v2/offload/compact`; must be in `(0, 2]`. | `0.5` |
| `TokenCounter` | Optional `model.TokenCounter` used for the local `CompactionRatio` trigger and compact request metadata; failures fall back to the simple estimator. | simple estimator |

## Notes

- Legacy mode encodes app, user, and session into `session_key`; this is not a
  hard tenant boundary. V3 sends the configured service/team/agent plus the
  framework user/session IDs. Automatic recall and `tdai_memory_search` remain
  opt-in to preserve existing behavior.
- V3 requests always carry a non-empty Bearer header. `WithAPIKey(...)` supplies
  the real key; when it is omitted, the adapter sends the non-secret `local`
  placeholder required by the self-hosted gateway parser. That placeholder is
  valid only when shared-secret authentication is disabled. If the gateway is
  started with `TDAI_GATEWAY_API_KEY`, configure the matching key or non-health
  routes return 401 while the health check still passes.
- `tdai_memory_search` searches extracted long-term memory; extraction is
  asynchronous, so newly captured facts may take a short time to become
  searchable.
- `tdai_conversation_search` searches conversation history in the current
  Legacy `session_key` or V3 `session_id`.
- Capture uses at-least-once delivery. The checkpoint advances only after the
  complete capture is acknowledged. An ambiguous gateway failure may therefore
  replay accepted L0 messages because the current V3 API has no client
  write-idempotency key.
- V3 capture truncates an individual L0 message that exceeds the gateway's
  8192-character limit and appends `...[truncated]`; later messages continue to
  be captured and the checkpoint can advance after the bounded request is
  acknowledged. V3 search queries are likewise bounded to the gateway's
  2048-character limit.
- Context offload is opt-in and gateway-owned. It uses only
  `/v2/offload/ingest`, `/v2/offload/compact`, and `/v2/offload/read-ref`; it
  does not call `/capture` or `/recall`.
- The gateway may replace archived tool results with messages such as
  "原始工具结果已存档，如需查看完整内容请调用 Offload V2 result_ref 恢复接口".
  Register `memSvc.Tools()` so the model can satisfy that instruction through
  `tdai_read_offload_ref`.
- The adapter does not create local refs, JSONL indexes, Mermaid files, or
  local offload state. Scope validation, storage ACLs, token limits, and
  persistence are gateway responsibilities.
- Call `Close()` on the service so background capture workers shut down cleanly.
