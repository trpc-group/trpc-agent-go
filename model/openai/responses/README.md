# OpenAI Responses adapter

Use `responses.New` when an application needs the OpenAI Responses API through
`model.Model`. Keep `openai.New` for Chat Completions. This adapter is
opt-in: the root framework keeps its Go 1.21 requirement and existing Chat
Completions defaults.

The module requires Go 1.25. Downstream modules that import it must use that
Go version or newer. When `WithAPIKey` and `WithBaseURL` are omitted, the
OpenAI SDK reads `OPENAI_API_KEY` and `OPENAI_BASE_URL`. Each request sends
the full client-managed conversation and uses `store=false` by default.
Ordinary `LLMAgent` and `Runner` use does not require reading or editing
`Message.Replay`.

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai/responses"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

llm := responses.New("gpt-5", responses.WithStore(false))
agent := llmagent.New(
    "responses-assistant",
    llmagent.WithModel(llm),
    llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
)
// Runner uses an in-memory session until Close when none is configured.
r := runner.NewRunner("responses-app", agent)
```

`WithAPIKey` and `WithBaseURL` still set an explicit key or endpoint. Those
options, extra fields, and lifecycle rules are under
[Configuration and lifecycle](#configuration-and-lifecycle).

Runnable programs and the user-level walkthrough:

- [examples/openairesponses](../../../examples/openairesponses)
- [Model guide (English)](../../../docs/mkdocs/en/model.md#openai-responses-api)
- [模型指南（中文）](../../../docs/mkdocs/zh/model.md#openai-responses-api)

The sections below are the detailed support, history, compatibility, and
recovery contract.

## Supported contract

- Full client-managed history on every request; `store=false` by default.
- Text, reasoning summaries, refusals, and ordered local function calls.
- Input text, images, and files; JSON schema output and supported generation
  parameters. Tool declarations are sent as non-strict function schemas.
- Streaming text, refusal, and reasoning previews, followed by one
  authoritative completed response. Tool calls are exposed only in that
  completed response. A completed response whose output array is empty is
  valid only when the stream has not already shown output. Non-empty text,
  refusal, and reasoning deltas, and native or function-call output events,
  make that empty completion an error. Earlier previews remain previews; they
  are not rebuilt into the final message, native items, or tool calls.
- API errors and incomplete/failed/cancelled responses produce error responses.
  Malformed streams and EOF without a terminal response are errors. Cancelling
  the request context closes the stream without committing partial calls.

Only `message`, `reasoning`, and `function_call` output item types are supported.
Unknown output types fail explicitly, including hosted-tool calls. Audio/video
input, hosted tools, tool search, background execution, approval interactions,
`conversation`, `previous_response_id`, and provider compaction are outside this
initial contract. Unsupported Chat-specific parameters fail validation.

## Unknown field, event, and item compatibility

These three cases have different contracts:

- Unknown JSON fields on supported native output items remain in the
  provider-owned replay payload. They survive `Message` JSON persistence and
  are sent unchanged with that native item on the next request. Treat them as
  opaque provider state; this adapter does not interpret them.
- Keepalives and unknown SSE event extensions are ignored by model projection
  and cannot commit content, tool calls, or replay state. Only
  a known `response.completed` event with a completed status and fully
  validated supported output commits a response. EOF after unknown events is an
  error.
- An unknown output-item type or nested message content-part type is a schema
  addition, not an unknown field. The adapter rejects the completion
  explicitly rather than dropping the item or treating it as executable
  output. Add adapter support before relying on a new item or content type.

## Native history and ownership

Streaming reasoning replay uses the complete item from `response.output_item.done`.
Its opaque `encrypted_content` can differ from the terminal response snapshot;
all other item fields must agree. The adapter still waits for a valid
`response.completed` before committing the response or executing tools.

Each completed response stores its entire ordered output array, including
opaque encrypted reasoning, annotations, item IDs, and message phase, in
`Message.Replay`. The adapter never synthesizes reasoning IDs, native items,
or tool calls from streamed deltas or a reasoning summary. It explicitly
requests encrypted reasoning so stateless continuation can retain provider
state. Additional include names are unioned with that required item and cannot
replace it.

The other `Message` fields are read-only projections for UI and local tool
execution. The framework records a fingerprint when binding native output;
editing text, roles, tool names, or arguments invalidates that binding, even
after JSON persistence. This detects inconsistent edits; it is not an
authentication signature. Applications should treat replay data as sensitive
conversation state despite its opaque encoding.

Replay v1 uses a fixed encoding for its message projection, including nested
content parts and tool-call data. This release keeps the persisted replay
format and fingerprint unchanged. If a future field can affect replay or tool
execution, define its binding explicitly and version the binding when needed;
serializing every `Message` field does not automatically change v1.

Persisted replay metadata with an unsupported version or invalid format must
not be silently dropped. Redis readers return decoding errors when they can
identify a replay-bearing record. If the entire event JSON is malformed, that
identification can fail and the record may be skipped under the existing Redis
history policy. A successful history read therefore does not guarantee that no
malformed records were skipped.

History processing treats each native record as one output unit: empty
records, item order, reasoning, call order, and call IDs are preserved, native
assistant messages are not merged with their neighbors, and their reasoning is
not stripped. The ordinary history pipeline otherwise applies unchanged:
tool-result compaction, oversized-result truncation, result reordering,
summary cutoffs, and `MaxHistoryRuns` behave as they do for legacy history.

- A completed prefix, including completed native tool rounds, may be replaced
  by a normal session summary. The summary is new semantic context, not a
  lossless continuation of the removed prefix, and cannot recover opaque
  reasoning that has been removed.
- When a summary cutoff runs through a native tool round, the original record
  and every result that answers its calls are restored together. Legacy
  history keeps restoring only the calls whose results follow the cutoff.
- `MaxHistoryRuns` still counts messages. Only a pending native round, a
  native call record followed solely by its tool results, may extend the
  limit, and only by that round. Completed native rounds are removed whole.
- `ToolTranscriptModeOmitPreviousCompleted` rebases completed tool rounds of
  earlier requests: their calls and results are removed, visible text remains
  as ordinary assistant history, and the native record and its reasoning are
  dropped. Rounds of the current request are never rebased.
- Native call arguments in a resumed tool round are not compacted; their tool
  results are.
- An incomplete per-call result round is kept intact for a native record
  instead of being stripped, so the adapter rejects the missing result
  explicitly rather than replaying a record without some of its calls.

Foreign-agent events converted into user context are newly authored user
messages without replay state. A legacy or differently configured downstream
agent can therefore consume a native agent's output as ordinary context, which
is a semantic rebase rather than a native continuation. With
`WithPreserveForeignMessages(true)`, foreign records keep their roles and
replay state, so only agents that consume the same protocol and model can use
them. Custom event projectors cannot remove replay state; rewriting a native
record fails validation instead of silently choosing between two
representations. Do not clear replay on pending tool turns.

LLM flows and Graph LLM nodes validate `model.Info.ReplayProtocol` and the
configured model name before handing native history to an adapter. Another
model or a legacy adapter is rejected; wrappers must forward `Info` correctly.
Both also validate a native response after model callbacks and argument
repair, before it is emitted, stored, or used to execute tools. After-model
callbacks and plugins may observe a native response, but editing, rebinding,
moving, dropping, or replacing its native records fails the call before any
tool runs. Adding or removing choices is also rejected. Return an error from
the callback to block such a response. Before-model callbacks receive detached
native history projections: edits to those projections leave the stored native
records and Graph input unchanged, and retaining Replay on an edited projection
rejects the request. Before-tool callbacks receive separate execution
arguments, so in-place edits and `ModifiedArguments` can affect the tool without
rewriting the original native call or its replay binding. The next model request
retains the original call and pairs its result with the same call ID. Legacy
responses keep the existing callback override behavior. Graph state
copies, checkpoints, and cache keys preserve native records. Direct callers of
other adapters must also avoid passing native records they cannot consume. A
switch to another endpoint using the same model name is the application's
responsibility; endpoint/account identity is not encoded in this record.
Built-in failover and hedge wrappers require native candidates to share the
same protocol and configured model name; a hedge logical name must also match.
Mixed native routing fails at construction instead of falling back to a lossy
representation.

### Tool recovery boundaries

With `agent.WithResume(true)`, a pending native tool response is validated
against the first selected model before any of its tools execute. Resume and
the following model request share that selection. Edited projections or a
different protocol/configured model name fail before tool execution. Legacy
Chat recovery keeps its existing execution order.

The existing resume mechanism only handles a final persisted assistant event
containing tool calls. It does not scan earlier events for missing results.
For example, if a response calls A and B and only A's result was persisted as
a separate event before a crash, resume does not run B. The incomplete native
round fails history validation; this adapter does not provide recovery for
partially completed multi-tool rounds.

Session persistence and business side effects are separate operations. A
successful tool side effect followed by a crash before its result is stored
can execute again on resume; use business idempotency where needed. Session
write errors are not transactional execution barriers in the existing Runner:
a failed tool-call event write can still be followed by tool execution. Do not
interpret a final answer as proof that all session events were durably stored.

The default token counter estimates serialized native payload size. It cannot
measure the provider's decrypted reasoning tokens. Custom counters must account
for native state themselves; context limits still need provider-side enforcement.
The payload and its display projection use additional session storage. No
compression or deduplication of the native payload is performed in this version.

## Compatibility

The existing non-native model path keeps its defaults. Messages without
replay data keep their request projection, JSON encoding, persistence,
routing, and Graph cache keys. Registering a Responses model that is never
selected does not change a legacy session. `model.Message` and `model.Info`
gained fields, so code that builds them with positional composite literals
must switch to keyed fields. This is a source-compatibility exception even for
applications that do not select Responses. An empty `Info.ReplayProtocol` is
omitted from JSON, preserving the existing encoding for legacy models.

An older framework that reads and rewrites a replay-bearing session can drop
the unknown replay field. Do not route native sessions through older writers;
readers that reject unknown JSON fields also need to be upgraded first.
Retaining unknown fields inside a native payload does not make a new native
item type compatible with old readers, even when the replay format version is
unchanged. Upgrade every reader and writer that handles replay-bearing history
before enabling a feature that emits a new item type. An older binary may strip
replay state, so resuming that history with the old binary cannot be promised.

This adapter requires a root module version containing `Message.Replay` and
`Info.ReplayProtocol`. Repository-relative `replace` directives support local
development only; downstream consumers must resolve a compatible root version
because dependency modules' `replace` directives are not inherited.
Publish the compatible root module first, update this module's required root
version, and verify an external consumer without a root-module `replace`
before publishing the adapter module.

## Configuration and lifecycle

`WithBaseURL` configures a compatible API endpoint. When `WithAPIKey` is
omitted, the SDK reads `OPENAI_API_KEY`. When `WithBaseURL` is omitted, the SDK
reads `OPENAI_BASE_URL`.
`WithStore(true)` changes remote storage only; the adapter still sends full
history. The caller owns its session service and must keep draining the
response channel or cancel the request context. The runnable examples pass an
explicit in-memory session and close it when the process exits. User-level
setup is in the
[English model guide](../../../docs/mkdocs/en/model.md#openai-responses-api)
and the
[Chinese model guide](../../../docs/mkdocs/zh/model.md#openai-responses-api).

Terminal API failures preserve the reported error code and, for stream error
events, the parameter name in `Response.Error.Code` and `Response.Error.Param`.
Missing values remain nil; an error without a message uses a fallback message.

`WithExtraFields` and `Request.ExtraFields` accept ordinary dot-separated JSON
paths for additional parameters such as `reasoning.summary` and `tool_choice`.
Colon prefixes and backslash escapes are rejected. Managed model, input, tool,
storage, and continuation fields stay protected.

The exact field `include` is additive, unlike other extra fields where a
request value replaces a model value. A missing value, nil, or an empty list
adds no names. Any other value must be an array of nonempty strings, given as `[]string`, a
decoded `[]any` of strings, or another slice or array of a string type. A Go
string or byte slice is rejected. The adapter copies those names and does not
modify the caller's maps or arrays. The wire value is
`reasoning.encrypted_content`, then names from the model option, then names
from the request, without duplicates; the first occurrence wins. High-level
fields cannot remove `reasoning.encrypted_content`. Nested, indexed, and alias
include paths are rejected before the request is sent. Supplying an include
name does not add logprobs mapping, hosted tools, or server-managed
conversation state.

```go
m := responses.New(modelName, responses.WithExtraFields(map[string]any{
    "include": []string{"message.output_text.logprobs"},
}))
// Request.ExtraFields["include"] can add further names for one call.
```

This adapter does not expose callbacks for native SDK requests, responses, or
stream events. Use the framework's model and tool callbacks for their supported
operations; Chat Completions SDK callbacks do not apply to this adapter. The
native-history mutation constraints above still apply to framework callbacks.
Low-level `WithOpenAIOptions` can bypass validation, including by replacing
`include`, so callers that use it must preserve the managed-state contract
themselves.

The tests use local HTTP fixtures and a real SQLite close/reopen cycle. They do
not demonstrate access to any live model, account, or compatible gateway.
