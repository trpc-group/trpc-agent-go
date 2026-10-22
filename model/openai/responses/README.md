# OpenAI Responses adapter

Use this independent Go 1.25 module when an application needs the OpenAI
Responses API through `model.Model`. The root framework keeps its Go 1.21
requirement and existing Chat Completions defaults.

```go
import "trpc.group/trpc-go/trpc-agent-go/model/openai/responses"

m := responses.New("gpt-5", responses.WithAPIKey(apiKey))
```

See [the chat and Runner examples](../../../examples/openairesponses).

## Supported contract

- Full client-managed history on every request; `store=false` by default.
- Text, reasoning summaries, refusals, and ordered local function calls.
- Input text, images, and files; JSON schema output and supported generation
  parameters. Tool declarations are sent as non-strict function schemas.
- Streaming text/reasoning previews, followed by one authoritative completed
  response. Tool calls are exposed only in that completed response.
- API errors and incomplete/failed/cancelled responses produce error responses.
  Malformed streams and EOF without a terminal response are errors. Cancelling
  the request context closes the stream without committing partial calls.

Only `message`, `reasoning`, and `function_call` output item types are supported.
Unknown output types fail explicitly, including hosted-tool calls. Audio/video
input, hosted tools, tool search, background execution, approval interactions,
`conversation`, `previous_response_id`, and provider compaction are outside this
initial contract. Unsupported Chat-specific parameters fail validation.

## Native history and ownership

Each completed response stores its entire ordered output array, including
opaque encrypted reasoning, annotations, item IDs, and message phase, in
`Message.Replay`. The adapter never synthesizes reasoning IDs or rebuilds
native output from a reasoning summary. It explicitly requests encrypted
reasoning so stateless continuation can retain provider state.

The other `Message` fields are read-only projections for UI and local tool
execution. The framework records a fingerprint when binding native output;
editing text, roles, tool names, or arguments invalidates that binding, even
after JSON persistence. This detects inconsistent edits; it is not an
authentication signature. Applications should treat replay data as sensitive
conversation state despite its opaque encoding.

History processing preserves empty native records, item order, and record
boundaries. Legacy assistant merging, reasoning stripping, result reordering,
and tool-result compaction are skipped when they would alter native history.
The active invocation survives a summary cutoff intact. History limits retain
a native call's user anchor when its results cross the limit or remain pending.
An entire completed prefix may be replaced by a normal session summary; it is
a new semantic context, not a lossless continuation of the removed prefix.
Summaries cannot recover opaque reasoning that has been removed.

Custom event projectors and tool-transcript modes that rewrite a native record
fail validation instead of silently choosing between two representations.
Foreign-agent events converted into user context are also not native
continuations. Intentionally rebase completed history using newly authored
messages with no replay state; do not clear replay on pending tool turns.

LLM flows validate `model.Info.ReplayProtocol` and the configured model name
before handing native history to an adapter. Another model or a legacy adapter
is rejected; wrappers must forward `Info` correctly. Direct callers of other
adapters must also avoid passing native records they cannot consume. A switch
to another endpoint using the same model name is the application's
responsibility; endpoint/account identity is not encoded in this record.
Built-in failover and hedge wrappers require native candidates to share the
same protocol and configured model name; a hedge logical name must also match.
Mixed native routing fails at construction instead of falling back to a lossy
representation.

The default token counter estimates serialized native payload size. It cannot
measure the provider's decrypted reasoning tokens. Custom counters must account
for native state themselves; context limits still need provider-side enforcement.
The payload and its display projection use additional session storage. No
compression or deduplication of the native payload is performed in this version.

## Configuration and lifecycle

`WithBaseURL` configures a compatible API endpoint. The SDK reads
`OPENAI_API_KEY` when no explicit API key is supplied. `WithStore(true)` changes
remote storage only; the adapter still sends full history. The caller owns its
session service and must keep draining the response channel or cancel the
request context.

`WithExtraFields` accepts SDK JSON paths for additional parameters such as
`reasoning.summary` and `tool_choice`. Managed model/input/tools/storage/
continuation fields are protected. The request callback may tune other typed
parameters; response and stream callbacks are read-only observers. Callbacks
must not retain mutable arguments. Low-level `WithOpenAIOptions` middleware can
bypass validation, so applications must preserve the managed-state contract.

The tests use local HTTP fixtures and a real SQLite close/reopen cycle. They do
not demonstrate access to any live model, account, or compatible gateway.
