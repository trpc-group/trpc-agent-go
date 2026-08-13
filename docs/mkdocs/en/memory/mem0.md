# External Long-Term Memory Integration (`mem0`)

`memory/mem0` integrates [mem0](https://mem0.ai), an externally hosted long-term memory platform. It is suitable when you want mem0 to handle memory extraction and storage, while the Agent continues to query memories through standard tools.

Unlike the built-in backends above, `memory/mem0` is **not** a full `memory.Service` implementation. It uses an ingest-first pattern: Runner forwards session transcripts to mem0 after each turn, mem0 performs extraction on the service side, and the Agent uses read-oriented tools to query the results.

**Use case**: Hosted long-term memory, background extraction after each turn, and no local CRUD write path.

## Configuration Example

```go
import (
    "os"

    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    memorymem0 "trpc.group/trpc-go/trpc-agent-go/memory/mem0"
    "trpc.group/trpc-go/trpc-agent-go/model/openai"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

mem0Svc, err := memorymem0.NewService(
    memorymem0.WithAPIKey(os.Getenv("MEM0_API_KEY")),
    memorymem0.WithLoadToolEnabled(true),
)
if err != nil {
    panic(err)
}
defer mem0Svc.Close()

sessionSvc := sessioninmemory.NewSessionService()
agent := llmagent.New(
    "assistant",
    llmagent.WithModel(openai.New("deepseek-v4-flash")),
    llmagent.WithTools(mem0Svc.Tools()),
    llmagent.WithPreloadMemory(10), // Optional read-only preload budget.
)

r := runner.NewRunner(
    "my-app",
    agent,
    runner.WithSessionService(sessionSvc),
    runner.WithSessionIngestor(mem0Svc),
)
defer r.Close()
```

**Integration points**:

- Register tools with `llmagent.WithTools(mem0Svc.Tools())`
- Use `runner.WithSessionIngestor(mem0Svc)` to send session transcripts to mem0
- Optionally enable `llmagent.WithPreloadMemory(N)`; because `mem0Svc` also implements `memory.Reader`, the runner can use it for read-only preload
- Do **not** use `runner.WithMemoryService(...)` with this integration

## Why `WithSessionIngestor(...)` Instead of `WithMemoryService(...)`

`runner.WithMemoryService(...)` is designed for built-in memory backends that implement the full `memory.Service` contract. In addition to read APIs, that contract includes framework-owned write semantics such as `AddMemory`, `UpdateMemory`, `DeleteMemory`, `ClearMemories`, and `EnqueueAutoMemoryJob(...)`.

`memory/mem0` has a different boundary. It does not expose the full CRUD lifecycle to the framework. Instead, it accepts a completed session transcript, forwards it to mem0 for hosted extraction, and then exposes read-oriented tools for retrieval.

Using `runner.WithSessionIngestor(...)` makes that boundary explicit:

- Runner sends the completed session transcript after each turn
- mem0 performs extraction and storage on the service side
- per-request ingest fields such as `metadata`, `agent_id`, and `run_id` can be passed through `session.IngestOption`
- the integration is not mistaken for a built-in backend that supports full framework-side CRUD; framework-side preload uses only the read-only `memory.Reader` surface when explicitly enabled

In short, `MemoryService` means "the framework manages memories directly", while `SessionIngestor` means "the framework hands the transcript to an external memory system". `mem0` matches the second model.

## Configuration Options

| Option | Purpose | Default |
| ------ | ------- | ------- |
| `WithAPIKey(key)` | mem0 API key. Required for hosted platform requests; optional for self-hosted OSS when auth is disabled. | required |
| `WithHost(url)` | Override the mem0 API host/base URL. | `https://api.mem0.ai` |
| `WithSelfHostedOSS()` | Use the self-hosted Mem0 OSS REST API (`/memories`, `/search`, `X-API-Key`). When enabled without `WithHost`, the host defaults to `http://localhost:8888`; the hosted-platform default host is rejected in OSS mode. | disabled |
| `WithSelfHostedOSSIncludeUnscopedMemories()` | Include legacy OSS records that do not carry `metadata.trpc_app_name`; records tagged for a different app remain hidden. | disabled |
| `WithSelfHostedIngestPrompt(prompt)` | Set the extraction prompt for every self-hosted ingestion request from this service. | server default |
| `WithSelfHostedIngestExpirationDateResolver(resolver)` | Resolve the `expiration_date` independently for each self-hosted ingestion request. | omitted |
| `WithIngestInference(bool)` | Control whether Mem0 extracts memories from transcripts. This applies to hosted and self-hosted ingestion. | `true` |
| `WithSelfHostedProceduralMemory()` | Create self-hosted procedural memories. An `agent_id` is required. | disabled |
| `WithOrgProject(orgID, projectID)` | Add hosted-platform `org_id` / `project_id`; unsupported with self-hosted OSS. | empty |
| `WithAsyncMode(bool)` | Controls hosted-platform `async_mode`; self-hosted OSS writes are synchronous at the REST layer. | `true` |
| `WithVersion(v)` | Sets the hosted-platform ingestion API version field. | `v2` |
| `WithTimeout(d)` | HTTP timeout used by the client. | `10s` |
| `WithLoadToolEnabled(bool)` | Expose `memory_load` from `Tools()`. | `false` |
| `WithAsyncMemoryNum(n)` | Number of background ingest workers. | `1` |
| `WithMemoryQueueSize(n)` | Queue size per ingest worker. | `10` |
| `WithMemoryJobTimeout(d)` | Timeout for queued jobs and synchronous fallback ingest. | `30s` |

## Self-Hosted OSS Request Fields

The standard Runner path supplies the session ID as `run_id` and the active
agent name as `agent_id`. Mem0-specific behavior is configured once when the
service is created, so `IngestSession` remains the only ingestion API:

| Mem0 OSS create field | Source |
| --------------------- | ------ |
| `messages` | The non-empty session delta selected by the ingestor. |
| `user_id` | `session.Session.UserID`. |
| `agent_id` | `session.WithIngestAgentID`; Runner supplies the active agent name. |
| `run_id` | `session.WithIngestRunID`; Runner supplies the session ID. |
| `metadata` | `session.WithIngestMetadata`, plus the internal tRPC app scope. |
| `prompt` | `WithSelfHostedIngestPrompt`. |
| `expiration_date` | `WithSelfHostedIngestExpirationDateResolver`. |
| `infer` | `WithIngestInference`; defaults to `true`. |
| `memory_type` | `WithSelfHostedProceduralMemory`; omitted for ordinary memories. |

```go
package example

import (
    "context"
    "time"

    memorymem0 "trpc.group/trpc-go/trpc-agent-go/memory/mem0"
    "trpc.group/trpc-go/trpc-agent-go/session"
)

func newProceduralMemoryService() (*memorymem0.Service, error) {
    expirationForSession := func(
        _ context.Context,
        sess *session.Session,
    ) (time.Time, error) {
        if sess.CreatedAt.IsZero() {
            return time.Time{}, nil
        }
        return sess.CreatedAt.AddDate(0, 0, 30), nil
    }

    return memorymem0.NewService(
        memorymem0.WithSelfHostedOSS(),
        memorymem0.WithHost("http://localhost:8888"),
        memorymem0.WithSelfHostedIngestPrompt(
            "Extract reusable deployment procedures.",
        ),
        memorymem0.WithSelfHostedIngestExpirationDateResolver(
            expirationForSession,
        ),
        memorymem0.WithSelfHostedProceduralMemory(),
    )
}

func newRawMemoryService() (*memorymem0.Service, error) {
    return memorymem0.NewService(
        memorymem0.WithSelfHostedOSS(),
        memorymem0.WithHost("http://localhost:8888"),
        memorymem0.WithIngestInference(false),
    )
}
```

`newRawMemoryService` stores adapter-normalized non-system message text
without LLM extraction. It deliberately uses a separate service without a
custom prompt or procedural memory. Mem0 still invokes its embedder to persist
and search these raw memories.

- `session.WithIngestMetadata`, `session.WithIngestAgentID`, and
  `session.WithIngestRunID` continue to set common fields for an individual
  `IngestSession` call. Runner supplies the agent and run IDs automatically.
- `WithSelfHostedIngestPrompt` forwards the service's extraction prompt on
  every self-hosted create request with inference enabled.
- `WithSelfHostedIngestExpirationDateResolver` runs once for each valid,
  non-empty ingestion before the watermark advances. The callback receives the
  request context and session, and returns a `time.Time`; its calendar date in
  that value's location is sent as `YYYY-MM-DD`. A zero value omits the field.
  An error aborts ingestion without sending a request or advancing the
  watermark. The callback may run concurrently and must treat the session as
  read-only. Expiration hides a memory from normal reads after the date; it does
  not delete the stored record.
- `WithIngestInference` controls Mem0's `infer` field. Its default remains
  `true`; `false` sends normalized non-system messages for direct import
  without LLM extraction and cannot be combined with a custom extraction
  prompt or procedural memory. Self-hosted OSS stores both user and assistant
  direct-import messages; the hosted platform currently retains only user-role
  direct-import messages. Static incompatible combinations are rejected by
  `NewService`.
- `WithSelfHostedProceduralMemory` selects Mem0's
  `procedural_memory` mode. Mem0's public create API otherwise infers ordinary
  memories; procedural memory requires an `agent_id` and always uses inference.
- Prompt, expiration-date resolver, and memory type are OSS-only and are
  rejected in hosted-platform mode rather than silently ignored. `infer` is
  supported in both modes.
- The pinned OSS REST create schema does not expose `timestamp`; the underlying
  `Memory.add` implementation marks a non-empty timestamp as platform-only and
  rejects it. This adapter therefore does not expose that field.
- These self-hosted request fields are validated against Mem0 OSS 2.0.11 at
  `mem0ai/mem0@3b9aed8`. Older OSS releases are not supported for these fields
  and may silently ignore request properties that their REST schema does not
  recognize.

Self-hosted reads and searches use the same `ReadMemories` and
`SearchMemories` methods as the hosted adapter. `MaxResults` caps the final
locally filtered result set. The adapter may request a larger `top_k` candidate
set so framework-side kind and time filters can still fill that result budget.
In self-hosted mode, a non-zero `SimilarityThreshold` is also forwarded as
`threshold`. Results are mapped to
`memory.Entry`, including ID, text, score, timestamps, and the structured
tRPC memory fields stored in metadata. Provider-only diagnostics that have no
representation in `memory.Entry` are intentionally not exposed as a second
public result model.

For the official self-hosted OSS server, configure the server-side LLM and
embedder independently when they use different endpoints or API keys. The OSS
server exposes `POST /configure`; set `llm.provider=openai` with the LLM model,
base URL, and API key, and set `embedder.provider=openai` with the embedding
model, base URL, and API key. The Go adapter only uses the REST API and does not
access the OSS server's internal vector store directly.

## Notes

- `Tools()` exposes `memory_search` by default; `memory_load` can be enabled explicitly.
- All reads remain scoped to the current `<appName, userID>`.
- Self-hosted OSS app isolation uses `metadata.trpc_app_name` because the OSS API has no top-level `app_id`. Existing OSS records without this metadata are hidden by default until reingested or backfilled. Use `WithSelfHostedOSSIncludeUnscopedMemories()` only for migrations that need those legacy records visible.
- The current OSS `GET /memories` API is capped at 1000 user-level results, is not pageable, and cannot express `metadata.trpc_app_name` as a server-side filter. `ReadMemories` therefore requires a positive limit no larger than 1000 and applies app isolation as a best-effort local filter over the first 1000 OSS records returned for the user.
- Runner automatically passes session context into `IngestSession`. Custom callers can use `session.WithIngestMetadata`, `session.WithIngestAgentID`, and `session.WithIngestRunID` for per-call common fields; Mem0-specific fields are service options.
- `WithPreloadMemory(N)` works with mem0 when the same service is configured via `runner.WithSessionIngestor(mem0Svc)`. Use a positive budget in production.
- When mem0 metadata is available, search results can still carry structured fields such as `Topics`, `Kind`, `EventTime`, `Participants`, and `Location`.
- Call `Close()` on the service so background workers shut down cleanly.
- If you need full CRUD tools, use one of the built-in memory backends instead.
