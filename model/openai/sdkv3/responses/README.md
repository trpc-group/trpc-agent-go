# OpenAI Responses adapter

Use `responses.New` when an application needs the OpenAI Responses API through
`model.Model`. This adapter is published from the standalone
`trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3` Go module for the
OpenAI Go SDK v3 generation. The `sdkv3` path identifies the SDK generation; the
adapter module has its own release version. The existing `model/openai`
package and `openai.New` remain available for Chat Completions. See
[module versioning and release](../README.md#module-versioning-and-release) for details.

This adapter is opt-in: the root framework keeps its Go 1.21 requirement and
existing Chat Completions defaults.

The module requires Go 1.25. Downstream modules that import it must use that
Go version or newer. When `WithAPIKey` and `WithBaseURL` are omitted, the
OpenAI SDK reads `OPENAI_API_KEY` and `OPENAI_BASE_URL`. Each request sends
the full client-managed conversation and uses `store=false` by default.
Ordinary `LLMAgent` and `Runner` use does not require reading or editing
`Message.ProviderData`.

```go
import (
    "trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
    "trpc.group/trpc-go/trpc-agent-go/model"
    "trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
    "trpc.group/trpc-go/trpc-agent-go/runner"
)

llm := responses.New("gpt-5")
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

- [examples/openairesponses](../../../../examples/openairesponses)
- [Model guide (English)](../../../../docs/mkdocs/en/model.md#openai-responses-api)
- [模型指南（中文）](../../../../docs/mkdocs/zh/model.md#openai-responses-api)

The sections below are the detailed support, history, compatibility, and
recovery contract.

## Supported contract

- Full client-managed history on every request; `store=false` by default.
- Text, reasoning summaries, refusals, and ordered local function calls.
- Input text, images, and files; JSON schema output and supported generation
  parameters. Tool declarations are sent as non-strict function schemas.
- Local function outputs containing text and images through explicit
  `RoleTool.ContentParts`; text-only tool outputs keep their string format.
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

Only `message`, `reasoning`, `function_call`, `tool_search_call`, and
`tool_search_output` output item types are supported; the tool search items
are described in [Tool search](#tool-search). Unknown output types fail
explicitly, including hosted-tool calls. Audio/video input, other hosted tools,
background execution, approval interactions, `conversation`,
`previous_response_id`, and provider compaction are outside this initial
contract. Unsupported Chat-specific parameters fail validation.

## Tool search

[Tool search](https://developers.openai.com/api/docs/guides/tools-tool-search)
lets the model load tool definitions when it needs them instead of receiving
every definition up front. It is opt-in and requires a model that supports
`tool_search`; the OpenAI guide lists `gpt-5.4` and later.

- `WithHostedToolSearch()` declares deferred tools with `defer_loading: true`
  and adds `{"type":"tool_search"}`. The API searches and loads tools inside
  the same response, which stays `Done`.
- `WithClientToolSearch(handler)` leaves deferred tools out of the request and
  adds a client `tool_search` tool whose arguments have a `query` string. When
  the model searches, `handler` returns tool entries to load and execute. It
  can select deferred candidates, replace their tool objects, or return tools
  from an external catalog. The adapter records a `tool_search_output` with
  their definitions. Search is available even when no deferred tools are
  registered. A nil handler makes `GenerateContent` fail.

The two options are mutually exclusive; the later one wins. Wrap a tool or
ToolSet with `tool/deferred` to select what is deferred, then register it with
the agent as usual. Other tools keep their own `tool.DeferredTool` preference.
Deferred tools stay registered, filtered, and executed by the agent; only the
request changes. In hosted mode, requests without deferred tools and without
tool search history are unchanged. Adapters without tool search, such as Chat
Completions, ignore deferral.

Tools registered and allowed for the request remain executable even before a
search loads their definitions. Use the existing tool filters and permission
callbacks to enforce access restrictions; deferral controls schema exposure.

```go
import "trpc.group/trpc-go/trpc-agent-go/tool/deferred"

llm := responses.New("gpt-5.4", responses.WithHostedToolSearch())
agent := llmagent.New(
    "support",
    llmagent.WithModel(llm),
    llmagent.WithTools([]tool.Tool{currentTime, deferred.Tool(shippingETA)}),
    llmagent.WithToolSets([]tool.ToolSet{
        // Defer one CRM member; the others stay visible up front.
        deferred.ToolSet(crm, deferred.WithFilter(tool.NewIncludeToolNamesFilter("list_orders"))),
        // Defer the whole billing ToolSet.
        deferred.ToolSet(billing),
    }),
)
```

With no options, every wrapped tool is deferred. `deferred.WithFilter` selects
which tools to defer. The filter receives the original tool at request time;
for ToolSets, that is the original member before the framework adds a name
prefix. It returns true to
defer the tool and false to keep it eager, overriding the wrapped tool's own
`tool.DeferredTool` preference in either case. `deferred.WithFilter(nil)` restores
the default of deferring every wrapped tool. If set more than once, the last
filter wins. The same ToolSet wrapper can be registered with
`WithActivatableToolSets`, and it applies the filter to members refreshed from
the original ToolSet, including MCP members. Wrapping does not open a new
connection; closing the wrapped ToolSet closes the original. A nil original
returns nil.

Agent and run tool filters receive the registered wrapper. `Declaration()`
and `tool.MetadataOf` remain available; a filter that needs the original
concrete type can follow the existing `Original() tool.Tool` convention,
unwrapping more than once if needed.

### Namespaces

A ToolSet with at least one deferred member is sent as a `namespace` named
after the ToolSet. Members keep their original names inside it, and eager
members are sent without `defer_loading`. The namespace description comes from
`tool.ToolSetDescriber`; an MCP ToolSet returns `ConnectionConfig.Description`.
Without one, the description is `Tools from the <name> tool set.` ToolSets
without deferred members, and standalone tools, keep their ordinary top-level
function names.

For a custom ToolSet, keep the description in the instance and implement the
optional `Description()` method. The following application-defined type can
be used for the `billing` value registered above; `lookupInvoice` and
`listInvoices` are existing `tool.Tool` values:

```go
import (
    "context"

    "trpc.group/trpc-go/trpc-agent-go/tool"
)

type staticToolSet struct {
    name        string
    description string
    tools       []tool.Tool
}

func (s staticToolSet) Tools(context.Context) []tool.Tool { return s.tools }
func (s staticToolSet) Close() error                     { return nil }
func (s staticToolSet) Name() string                     { return s.name }
func (s staticToolSet) Description() string              { return s.description }

// In the agent setup, before registering the ToolSet:
billing := staticToolSet{
    name:        "billing",
    description: "Look up invoices, payments, and refunds.",
    tools:       []tool.Tool{lookupInvoice, listInvoices},
}
```

The deferred wrapper forwards `Description()`, and the adapter uses its value
as `namespace.description` and `ToolSearchEntry.NamespaceDescription`.

A function call returned with a namespace is mapped back to the tool that the
agent registered for that member, for example `crm_lookup` for `lookup` in
`crm`. The mapping uses the stored namespace and member name, without parsing
the registered name. Registered names must remain unique across ToolSets and
standalone tools; namespace grouping does not resolve collisions in the
agent's name-keyed tool registry. The native item keeps its namespace for the
next request. An unknown namespaced call becomes a not-found tool call named
`<namespace>.<name>` and
never falls back to a tool with the bare name. A namespace name that equals a
top-level function name fails the request. After an `AfterModel` payload edit,
the flow uses `ProjectProviderDataWithRequest` to resolve namespaced calls
against the request's tool bindings, including original-name ToolSets. An edit
to function arguments preserves the executor selected for that call ID,
namespace, and function name. If a callback introduces a call such as
`weather.weather` that could select two executors, reprojection reports
ambiguity before execution. To explicitly select a top-level function, use
its name with an empty namespace. Direct
`ProjectProviderData` calls have no request, so they reject namespaced calls.

### Client-executed search

The handler returns entries containing both the model-facing identity and the
tool object to execute:

```go
type ToolSearchHandler func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error)

type ToolSearchEntry struct {
    Namespace            string
    NamespaceDescription string
    Name                 string
    Tool                 tool.Tool
}
```

The handler receives a `ToolSearchRequest`:

- `CallID` is the `tool_search_call` it answers.
- `Arguments` holds the raw arguments, and `Query` holds their `query` string.
- `Candidates` lists deferred tools available to load. A registered candidate
  becomes available again when its replacement binding expires.

Candidates come from the request tools, so agent and run tool filters apply
before they reach the handler. Read a candidate's declaration through
`entry.Tool.Declaration()`. The model calls the function by its `Namespace`
and `Name`. For a ToolSet member such as `billing.lookup_invoice`, `Name`
is `lookup_invoice` even if the registered tool is named `billing_lookup_invoice`.
A new entry can omit `Name` to use the tool's declaration name. An empty
`Namespace` declares a top-level function.

This handler returns matching invoice candidates or loads an external tool
when the local catalog has no match.

```go
import (
    "context"
    "strings"

    "trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
    "trpc.group/trpc-go/trpc-agent-go/tool"
)

func invoiceSearch(catalogTool tool.Tool) responses.ToolSearchHandler {
    return func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
        if !strings.Contains(strings.ToLower(req.Query), "invoice") {
            return nil, nil
        }
        var entries []responses.ToolSearchEntry
        for _, entry := range req.Candidates {
            if strings.Contains(strings.ToLower(entry.Tool.Declaration().Description), "invoice") {
                entries = append(entries, entry)
            }
        }
        if len(entries) == 0 {
            entries = append(entries, responses.ToolSearchEntry{
                Namespace:            "external_billing",
                NamespaceDescription: "Invoice lookup tools.",
                Tool:                 catalogTool,
            })
        }
        return entries, nil
    }
}

// catalogTool is an application-owned tool.Tool with an invoice declaration.
llm := responses.New("gpt-5.4", responses.WithClientToolSearch(invoiceSearch(catalogTool)))
```

To substitute a sandbox implementation, copy a candidate and set
`entry.Tool = sandboxTool` before returning it. The model calls the same
namespace and function name with the sandbox tool's schema. The sandbox tool
receives those arguments through the usual permissions and tool callbacks.
`Run.ToolFilter` also applies to replacements and external tools. Each returned
entry needs a tool with a declaration and a distinct model-facing identity.
If two entries in one result map to the same execution key, the search
fails before either entry can be used. A nil or empty slice reports a search
with no matches.

The agent borrows returned objects for this Run. The application owns their
resources. Session history keeps the schema seen by the model. In a later Run,
an original candidate can execute through the agent's registered tool when
its namespace and function name still match, even if the ToolSet's naming
mode has changed. A call to a replacement or external tool from an earlier Run returns a tool error
asking the model to search again. If that tool is now registered as deferred
under the same namespace and name, the handler can select it from
`Candidates`. Later searches use this latest selection.

The candidate list stays local. With its default prompt and a fixed tool
configuration, LLMAgent includes its tool guidance from the first request,
even with an entirely external catalog. Search results append tool definitions
to the input history.

When using `GenerateContent` directly, check `IsFinalResponse()` for completion.
A pending client search has `Done == false`. Its choice has a nil `FinishReason`
unless it also contains ordinary function calls, which use `"tool_calls"`.
LLMAgent completes the search through `model.ProviderActionCompleter` before
running those functions. `CompleteProviderActions` runs the handler and returns
one tool message per search:

- `ToolID` is the call ID and `ToolName` is `ToolSearchToolName`.
- `Content` is a display summary such as `{"loaded_tools":["billing_lookup"]}`.
- `ProviderData` holds the complete `tool_search_output`, including the
  loaded definitions.

The flow publishes and persists that message, and model callbacks see the
search response, before the next model step. Tool callbacks do not run for a
search because it is not a tool call.

Loaded definitions stay in history, and the top-level tools of later requests
remain the eager tools plus `tool_search`. A loaded tool stays loaded while
its `tool_search_output` remains in the request history. A history limit or
summary that drops the output unloads the tool, and it becomes a candidate
again.

If the handler fails, LLMAgent emits an error and ends the run. Both the
unanswered search and any ordinary function calls in that response stay
pending. Before sending another user message, resume the same session with
`model.Message{}` and `agent.WithResume(true)`. Once the search succeeds,
LLMAgent executes the pending functions and sends their results and the
search output in the next model request.

If some search results were persisted before an interruption, resume reuses
their loaded definitions and retries only the unanswered searches.

To let the model continue without the tools, return an empty result instead
of an error. Canceling the run context stops the search without an output or a
search error. The handler may be called concurrently for different requests.

### Tool search boundaries

- Client search needs a flow that completes provider actions. LLMAgent and
  Runner do. Graph LLM nodes report `model response requests provider
  actions, which graph LLM nodes do not complete`. Hosted search there applies
  to tools implementing `tool.DeferredTool`, including wrapped tools.
- Model wrappers that do not forward `model.ProviderActionCompleter`, including
  the current failover and hedge wrappers, cannot complete client searches.
  LLMAgent then stops with `model "..." does not complete provider actions`.
- Resume is not exactly-once. A crash after the handler returns and before its
  output is stored runs the handler again. Resume applies the session history
  policy to events preceding the pending response, then reuses results already
  saved for that response. Resume candidates exclude loaded tools with usable
  bindings. A deferred registration can be selected again
  when its previous binding expired.
- A response can contain both a client search and function calls. If only
  some ordinary function results were stored, resume does not run the
  remaining function calls, as described under
  [Tool recovery boundaries](#tool-recovery-boundaries).
- An `AfterModel` callback must keep `Done` false on a client search response.
  Marking it `Done` skips the search, and the next request fails because the
  call has no output.
- Partial ToolSet deferral in client mode sends the eager members in a
  top-level namespace and later loads deferred members into a namespace with
  the same name. Top-level tool definitions stay unchanged across the search
  and execution requests.
- A run-scoped profile that overrides tool declarations, as in prompt
  iteration, changes the model-facing declaration but preserves the tool's
  deferral decision.
- Do not combine native tool search with `plugin/toolsearch`. The plugin has
  its own deferred-tool registration and does not use `tool.DeferredTool`.

## Images returned by local tools

Use `ToolResultMessages` to return text and images in one `RoleTool` message.
Keep the `ToolID` from `DefaultToolMessage`. The adapter sends the parts as
`function_call_output.output`, associated with the original `call_id`.

```go
type imageResult struct {
    Data   []byte `json:"-"` // Raw bytes, omitted from default JSON text.
    Format string `json:"format"`
}

callbacks := tool.NewCallbacks().RegisterToolResultMessages(
    func(_ context.Context, in *tool.ToolResultMessagesInput) (any, error) {
        if in.ToolName != "read_image" {
            return nil, nil
        }
        result, ok := in.Result.(*imageResult)
        if !ok || result == nil {
            return nil, nil
        }
        msg, ok := in.DefaultToolMessage.(model.Message)
        if !ok {
            return nil, fmt.Errorf("unexpected default tool message type %T", in.DefaultToolMessage)
        }
        msg.Content = "Image returned by read_image."
        msg.AddImageData(result.Data, "auto", result.Format)
        return msg, nil
    },
)
// Set llmagent.WithToolCallbacks(callbacks) on the Responses agent.
```

This snippet uses `context`, `fmt`, and the framework's `model` and `tool`
packages. The [runnable ReadImage example](../../../../examples/openairesponses/imagetool)
resolves an image ID to a configured local file. Implement image-ID resolution,
authenticated downloads, and image size limits in the application as needed.

- With at least one image part, non-empty `Content` becomes the first
  `input_text` block, followed by `ContentParts` in order. Text-only results keep
  the existing string format, including concatenated text parts.
- `AddImageData` accepts raw bytes and a format such as `png` or `image/png`.
  The adapter base64-encodes the bytes once. Use `AddImageURL` for a URL or an
  already encoded `data:image/...;base64,...` URL. As for user images, a
  non-empty image URL takes precedence over data; absent format defaults to PNG.
- JSON arrays and base64 strings in `Content` remain ordinary text. Convert
  application-specific result formats explicitly in the callback. Files,
  audio, and video in tool outputs remain unsupported.
- Keep binary data out of default JSON text and use a concise `Content`
  description to avoid also sending the image as text. History retention and
  compaction policies still apply; retained image bytes are persisted as part
  of the message.
- The model and gateway must support images in Responses function outputs.

## Unknown field, event, and item compatibility

These three cases have different contracts:

- Unknown JSON fields on supported native output items remain in the
  provider-owned native payload. They survive `Message` JSON persistence and
  are sent unchanged with that native item on the next request. Treat them as
  opaque provider state; this adapter does not interpret them.
- Keepalives and unknown SSE event extensions are ignored by model projection
  and cannot commit content, tool calls, or ProviderData. Only
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
`Message.ProviderData`. The adapter never synthesizes reasoning IDs, native items,
or tool calls from streamed deltas or a reasoning summary. It explicitly
requests encrypted reasoning so stateless continuation can retain provider
state. Additional include names are unioned with that required item and cannot
replace it.

`Message.ToolCalls` supplies the arguments for local tool execution. To override
them for an execution, return `tool.BeforeToolResult.ModifiedArguments` from a
`BeforeTool` callback. The next model request uses the native call from
`ProviderData` and pairs it with the tool result by call ID.

`Content` and `ReasoningContent` are editable display fields. The session also
persists `ProviderData`, which supplies the native items for the next model
request. Redacting display fields alone leaves the original text in native
history.

### Editing native output

The adapter sets `Protocol` to `openai.responses` and `Model` to the configured
model name. Keep both fields when editing native output. Decode `Payload` using
your SDK, edit the items, and assign the encoded JSON back to `Payload`.

The following `AfterModel` callback uses the official SDK to edit calculator
arguments. Unedited items keep their original JSON. Use
`ResponseOutputMessageParam` when editing a message so its ID, status, and phase
remain present. SDK serialization of an edited item may omit fields unknown to
that SDK version; it is not a general lossless JSON editor.

```go
import (
    "context"
    "encoding/json"

    sdkresponses "github.com/openai/openai-go/v3/responses"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

func rewriteCalculator(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
    if args.Error != nil || args.Response == nil || args.Response.IsPartial ||
        len(args.Response.Choices) == 0 {
        return nil, nil
    }
    msg := &args.Response.Choices[0].Message
    if msg.ProviderData == nil {
        return nil, nil
    }
    var items []sdkresponses.ResponseOutputItemUnion
    if err := json.Unmarshal(msg.ProviderData.Payload, &items); err != nil {
        return nil, err
    }
    output := make([]json.RawMessage, len(items))
    for i, item := range items {
        output[i] = json.RawMessage(item.RawJSON())
        fn, ok := item.AsAny().(sdkresponses.ResponseFunctionToolCall)
        if !ok || fn.Name != "calculator" {
            continue
        }
        var call sdkresponses.ResponseFunctionToolCallParam
        if err := json.Unmarshal(output[i], &call); err != nil {
            return nil, err
        }
        call.Arguments = `{"a":9}`
        encoded, err := json.Marshal(call)
        if err != nil {
            return nil, err
        }
        output[i] = encoded
    }
    encoded, err := json.Marshal(output)
    if err != nil {
        return nil, err
    }
    msg.ProviderData.Payload = encoded
    return nil, nil
}

// Register on the Responses agent using llmagent.WithModelCallbacks(calculatorCallbacks()).
func calculatorCallbacks() *model.Callbacks {
    return model.NewCallbacks().RegisterAfterModel(rewriteCalculator)
}
```

After the completed `AfterModel` callback chain finishes, an edited native
record is projected again before the response is emitted, persisted, or used
for local tool execution. The new payload updates `Content`, `ContentParts`,
reasoning fields, `ToolCalls`, and the choice finish reason. If a callback edits
both the payload and its projections, the payload takes precedence. Unchanged
native records keep callbacks' projection-only edits. Ordinary Chat Completions
responses retain their existing callback behavior.

The framework stores the record under `provider_data` with `provider`, `model`,
and `payload` keys. It checks the protocol and configured model before
continuation. Models implement the optional
`model.ProviderDataProjector` capability to project edited native records;
`model.ProviderDataRequestProjector` also receives the request to resolve
namespaced tool identities. Wrappers must forward these capabilities to support
such edits. Built-in failover and hedge wrappers do so. `BeforeModel` edits
affect only the current request's detached history, while `BeforeTool` edits
affect only the current tool execution.

Redis retains its existing skip-on-decode-error history policy, including
events with invalid ProviderData. Each history read logs at most one warning
when skipping malformed events. A successful read does not guarantee that no
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
  with its existing results. See [Tool recovery boundaries](#tool-recovery-boundaries)
  for how a later user turn handles missing results.

Foreign-agent events converted into user context are newly authored user
messages without ProviderData. A legacy or differently configured downstream
agent can therefore consume a native agent's output as ordinary context, which
is a semantic rebase rather than a native continuation. With
`WithPreserveForeignMessages(true)`, foreign records keep their roles and
ProviderData, so only agents that consume the same protocol and model can use
them. Custom event projectors cannot remove ProviderData from an unfinished native
tool round and still rely on recovery; the framework does not promise recovery
after ProviderData is removed from a pending round.

LLM flows and Graph LLM nodes validate `model.Info.ProviderDataProtocol` and the
configured model name before handing native history to an adapter. Another
model or a legacy adapter is rejected; wrappers must forward `Info` correctly.
Both allow callbacks to edit ProviderData on a completed response before it is
stored. When a callback edits the native payload, its regenerated `ToolCalls` determine
current execution and the same payload supplies subsequent wire input. Native item structure and
call/result pairing remain validated. Before-model callbacks edit a detached
copy of native history for the current model request. Before-tool
callbacks receive separate execution arguments, so in-place edits and
`ModifiedArguments` affect the current tool call without rewriting ProviderData. The
next model request sends ProviderData and pairs the result with the same call ID.
Legacy responses keep the existing callback override behavior. Graph state
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
the following model request share that selection. A `ToolCalls` edit can
change the resumed execution arguments; the following request still sends the
saved ProviderData payload. A different protocol or configured model name fails
before tool execution. Legacy Chat recovery keeps its existing execution
order.

**Known limitation of explicit resume.** For ordinary function calls, Runner only handles a
final persisted assistant event containing tool calls. It does not scan
earlier events for missing function results.
For example, if a response calls A and B and only A's result was persisted as
a separate event before a crash, explicit resume reports B's missing result
before sending a Responses request.

A new top-level user message in the same session lets Runner and LLMAgent continue after
an interruption. For ordinary native function calls, the outgoing request keeps
real results, including empty results and late results that can be attributed
to a call unambiguously. It adds a `function_call_output` for each missing
result stating that its execution status is unknown. The framework neither
reruns those calls nor writes the placeholders to the session. This also
works for existing sessions with a retained tool call and no recorded result.
Wrappers must forward `model.ProviderDataProjector` so the framework can pair
results with the IDs in the native record. For tool namespaces, wrappers must
also forward `model.ProviderDataRequestProjector` to resolve registered names.

An empty explicit resume continues to handle the current pending round as
described above. If an older incomplete round has already been superseded by a
later user message, its missing results are repaired in the outgoing request
without rerunning its calls. Chat, `tool_search`, and other provider actions
retain their existing behavior. Direct adapter calls and Graph LLM nodes
continue to validate the history supplied to them.

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
ProviderData keep their request projection, JSON encoding, persistence,
routing, and Graph cache keys. Registering a Responses model that is never
selected does not change a legacy session. `model.Message` and `model.Info`
gained fields, so code that builds them with positional composite literals
must switch to keyed fields. This is a source-compatibility exception even for
applications that do not select Responses. An empty `Info.ProviderDataProtocol` is
omitted from JSON, preserving the existing encoding for legacy models.

An older framework that reads and rewrites a session containing ProviderData
can discard the unknown `provider_data` field. Do not route native sessions
through older writers; readers that reject unknown JSON fields also need to be
upgraded. Preserving opaque bytes does not make a new native item type
compatible with an old adapter reader. Upgrade every reader and writer that can
access the same sessions to versions that understand `provider_data`. An older
binary may strip ProviderData, so resuming that history through the old binary
cannot be promised.

This adapter requires a root module version containing `Message.ProviderData` and
`Info.ProviderDataProtocol`. Tool search additionally requires a root version
containing `model.ProviderActionCompleter`, `model.ProviderDataRequestProjector`,
and `tool.ToolSetDescriber`; the examples above also use `tool/deferred`. The
adapter uses root internal packages, so its required root version must be the
matching release rather than any later root version. Repository-relative
`replace` directives support local development only; downstream consumers
must resolve a compatible root version
because dependency modules' `replace` directives are not inherited. Publish
the compatible root module first, update this module's required root version,
and verify an external consumer without a root-module `replace` before
publishing the adapter module. See the [v3 module release notes](../README.md)
for the module and SDK version relationship and tag format.

## Configuration and lifecycle

`WithBaseURL` configures a compatible API endpoint. When `WithAPIKey` is
omitted, the SDK reads `OPENAI_API_KEY`. When `WithBaseURL` is omitted, the SDK
reads `OPENAI_BASE_URL`.
`WithStore(true)` changes remote storage only; the adapter still sends full
history. The caller owns its session service and must keep draining the
response channel or cancel the request context. The runnable examples pass an
explicit in-memory session and close it when the process exits. User-level
setup is in the
[English model guide](../../../../docs/mkdocs/en/model.md#openai-responses-api)
and the
[Chinese model guide](../../../../docs/mkdocs/zh/model.md#openai-responses-api).

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

## Testing

Adapter unit tests stay in this module. Runner, Graph, and Session integration
tests live in `test/openairesponses`, within the repository's separate E2E module.
From the repository root, run:

```sh
(cd model/openai/sdkv3 && go test ./...)
(cd test && CGO_ENABLED=1 go test ./openairesponses)
```

Both modules require Go 1.25 or newer. The integration tests also require a C
compiler for SQLite. They use local HTTP fixtures and real SQLite close/reopen
cycles, without API credentials or live model calls.
