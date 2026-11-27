# OpenAI Responses API examples

## Purpose

This independent module shows how to use the OpenAI Responses adapter with
`LLMAgent` and `Runner`. The adapter currently pins OpenAI SDK `v3.74.0`.

- `chat` is a multi-turn conversation without tools.
- `tools` registers a local calculator and keeps one in-memory session across
  user messages.
- `imagetool` uses `ToolResultMessages` to return a local image directly to
  the main model in a native function output.
- `toolsearch` defers support tools behind native tool search. It supports hosted
  search (`-mode server`) and client-executed keyword (`-mode client`) or
  semantic (`-mode semantic`) search. See
  [Tool search](#tool-search).

Every request sends the full client-managed conversation. The adapter defaults
to `store=false`. In ordinary Runner use, application code does not read or edit
`Message.ProviderData`.

## Prerequisites

- Go 1.25 or later. This directory is its own module so the Responses adapter
  can require Go 1.25 without raising the root module's Go 1.21 requirement.
  Run the commands below from this directory.
- `OPENAI_API_KEY` exported in the environment.

The `replace` directives in this module are for development inside this
repository. A downstream module must depend on a released root version that
contains the Responses history types.

## Configuration

The OpenAI SDK reads the API key and base URL from the environment.

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key. Required. Read by the OpenAI SDK. | |
| `OPENAI_BASE_URL` | API base URL. Read by the OpenAI SDK when set. | `https://api.openai.com/v1` |

| Flag | Description | Default |
| --- | --- | --- |
| `-model` | Model name passed to `responses.New`. | `gpt-5`; `gpt-5.4` for `toolsearch` |
| `-streaming` | Stream assistant text. | `true` |
| `-mode` | `toolsearch` only. `server` runs hosted search, `client` uses keyword matching, and `semantic` uses embeddings. | `server` |

`go run ./chat -help`, `go run ./tools -help`, and `go run ./toolsearch -help`
print those flags and do not call the API.

## Run

```bash
cd examples/openairesponses
export OPENAI_API_KEY="your-api-key"

# Streaming chat.
go run ./chat

# Non-streaming chat.
go run ./chat -streaming=false

# Streaming calculator. The default model is gpt-5.
go run ./tools

# Non-streaming calculator with an explicit model.
go run ./tools -model gpt-5 -streaming=false

# Hosted tool search, streaming.
go run ./toolsearch -mode server

# Client-executed keyword search, non-streaming.
go run ./toolsearch -mode client -streaming=false

# Client-executed semantic search using OpenAI embeddings.
go run ./toolsearch -mode semantic
```

Type `/exit` to quit. An empty line is ignored.

In `tools`, use two separate turns in the same process:

```text
Calculate 123 * 456.
Add 789 to that result.
```

The calculator performs one addition or multiplication per call. The same
in-memory session retains the first result for the second turn. Restarting
the process starts a new session. Nothing is written to disk.

## Expected interaction

The transcript below is illustrative, not a live run. The two `You:` prompts
are separate turns, and the process-local session still holds the first
result for the second turn. Assistant wording and the number of calls vary
by model. Streaming prints assistant text as tokens
arrive. Matching completed text is not repeated; if the completed text differs
from the preview, it is printed on a new `Assistant (completed):` line.
Non-streaming prints the completed assistant message once. Tool lines, when a
call is committed, use the program's format: name, original argument string,
and call ID come from the completed `Message.ToolCalls`, and the result line
is the Runner's committed tool message. The IDs below are placeholders.
`123 * 456` is `56088`, and `56088 + 789` is `56877`; that JSON is the
calculator's return value for those operations.

```text
OpenAI Responses with a local calculator. Type /exit to quit.
You: Calculate 123 * 456.
Tool call: calculator (ID: call_multiply)
  Args: {"operation":"multiply","a":123,"b":456}
Tool result: calculator (ID: call_multiply): {"value":56088}
Assistant: 123 * 456 is 56088.
You: Add 789 to that result.
Tool call: calculator (ID: call_add)
  Args: {"operation":"add","a":56088,"b":789}
Tool result: calculator (ID: call_add): {"value":56877}
Assistant: Adding 789 gives 56877.
You: /exit
```

If the model writes text before a call, that text is closed on an
`Assistant:` line, the tool lines follow, and later text starts a new
`Assistant:` line.

`chat` uses the same prompt loop without tool lines:

```text
OpenAI Responses chat. Type /exit to quit.
You: Hello
Assistant: Hello.
You: /exit
```

## ReadImage tool

Use a model and gateway that accept image blocks in Responses function outputs:

```bash
go run ./imagetool -model your-vision-model -image /path/to/image.png \
  -prompt "Read the visible text and describe the image."
```

The example instructs the model to call `read_image` with image ID `sample`.
The tool resolves that ID to the configured file. On a successful call,
`ToolResultMessages` places the raw bytes in `RoleTool.ContentParts`. The
adapter includes the image in the next request as an `input_image` block in
`function_call_output.output`, paired with the original call ID.

This example uses non-streaming responses. Use PNG, JPEG, static GIF, or WebP
images supported by your provider; the example checks the MIME type and limits
the file to 10 MiB. The result's byte field is excluded from default
JSON text. Replace the local lookup with your application's image-ID resolver
or authenticated download as needed; pass raw bytes rather than encoding them
again. For an existing image URL or data URL, use `Message.AddImageURL` instead.

The [tool image documentation](../../model/openai/sdkv3/responses/README.md#images-returned-by-local-tools)
describes ordering, text compatibility, and history behavior.

## Core API

```go
llm := responses.New(modelName)
agent := llmagent.New(
    "responses-tool-agent",
    llmagent.WithModel(llm),
    llmagent.WithDescription("An assistant that can use a local calculator."),
    llmagent.WithInstruction("Use the calculator for arithmetic. Each call performs one operation; use another call when the answer needs multiple operations."),
    llmagent.WithGenerationConfig(model.GenerationConfig{Stream: streaming}),
    llmagent.WithTools([]tool.Tool{calculatorTool()}),
)
sessions := sessioninmemory.NewSessionService()
r := runner.NewRunner(
    "responses-tools-example",
    agent,
    runner.WithSessionService(sessions),
)
```

`chat` is the same shape without `WithTools`. `calculatorTool` is the local
function in `tools/main.go`.

## Tool search

`toolsearch` registers a support assistant with these tools:

| Tool | Registration | Exposure |
| --- | --- | --- |
| `current_time` | `WithTools` | Eager |
| `shipping_eta` | `WithTools` | Deferred by `deferred.Tool` |
| `crm` ToolSet: `lookup_customer`, `list_orders` | `WithToolSets` | Only `list_orders` deferred |
| `billing` ToolSet: `lookup_invoice`, `list_invoices` | `WithToolSets` | Whole ToolSet deferred |

The program only configures the model, the tools, and the deferral policy:

```go
import "trpc.group/trpc-go/trpc-agent-go/tool/deferred"

search := responses.WithHostedToolSearch() // -mode server
// search := responses.WithClientToolSearch(keywordSearch) // -mode client
agent := llmagent.New(
    "responses-tool-search-agent",
    llmagent.WithModel(responses.New(modelName, search)),
    llmagent.WithTools([]tool.Tool{currentTimeTool(), deferred.Tool(shippingETATool())}),
    llmagent.WithToolSets([]tool.ToolSet{
        deferred.ToolSet(crmToolSet(), deferred.WithFilter(tool.NewIncludeToolNamesFilter("list_orders"))),
        deferred.ToolSet(billingToolSet()),
    }),
)
```

Without options, `deferred.Tool` and `deferred.ToolSet` defer every wrapped
tool. Use `deferred.WithFilter` for selective deferral.

The `crm` and `billing` ToolSets are sent as namespaces. They are application
`staticToolSet` values: their `description` field is returned by `Description()`
and becomes the namespace description. `lookup_customer` and `current_time`
are callable from the first request. In server mode, the API searches the deferred definitions
and loads the matching ones inside the same response, so only the loaded
tool calls are printed. In client mode, the deferred definitions are not sent.
`keywordSearch` matches whole words in the namespace name, tool name, and
tool description. It gives tool-name matches extra weight and returns
the entries of candidates scoring close to the best match. The handler has
signature `func(context.Context, *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error)`.
It reads descriptions through `entry.Tool.Declaration()` and returns the
selected entries unchanged. A request for a
single invoice selects `billing.lookup_invoice`. A request for orders and
delivery can select both `crm.list_orders` and `shipping_eta`. The example
prints the selected IDs in the `tool_search` result, then shows each tool call:

```text
OpenAI Responses tool search (client mode). Type /exit to quit.
You: Is invoice inv_7 paid?
Tool result: tool_search (ID: call_search): {"loaded_tools":["billing_lookup_invoice"]}
Tool call: billing_lookup_invoice (ID: call_invoice)
  Args: {"invoice_id":"inv_7"}
Tool result: billing_lookup_invoice (ID: call_invoice): {"invoice_id":"inv_7","status":"paid","amount":120}
Assistant: Invoice inv_7 is paid; the amount was 120.
You: /exit
```

The transcript is illustrative; queries, loaded tools, and wording vary by
model. The tool data is fixed sample data.

Semantic mode constructs a handler with `toolsearch.NewSemantic(openaiembedder.New())`
and registers it with `responses.WithClientToolSearch`. The default OpenAI
embedder uses `text-embedding-3-small`. The handler sends each query and
uncached candidate metadata to the embedding API, which adds requests and can
incur charges. Candidate vectors are cached for the life of the example
process. See the [semantic handler documentation](../../model/openai/sdkv3/responses/README.md#semantic-search-handler)
for ranking options or use a custom handler with `WithClientToolSearch`.
If a client search fails, type `/resume` to retry the pending turn in the
same session before entering another message.

Support boundaries and fallback semantics:

- Tool search needs a model that supports it; the OpenAI guide lists `gpt-5.4`
  and later. Other models reject the request; the example has no fallback.
- Deferral is exposure only. Deferred tools are still registered, filtered,
  and executed by the agent. With Chat Completions or without a tool search
  option, the same agent sends every tool up front.
- A loaded definition stays loaded while its search output remains in the
  session history. Returned executable objects are borrowed for the current
  Run, and the application owns their cleanup. Registered tools in this example
  remain available for later Runs. A later Run that calls a replacement or
  external tool receives a tool error asking the model to search again. Its
  handler can then return a fresh object.
- Client handlers can copy a candidate and set `entry.Tool = sandboxTool`, or
  return a new `responses.ToolSearchEntry{Tool: catalogTool}` for an external
  tool. Candidates' `Name` values are already wire names. A new entry can omit
  `Name` to use the tool's declaration name. Client search is available even
  without registered deferred tools.
  `Run.ToolFilter` also checks returned replacement and external objects, and
  normal execution permissions and tool callbacks apply.
- In client mode, a handler error ends the run before ordinary functions in
  the same response execute. Resume the same session with `model.Message{}`
  and `agent.WithResume(true)` before sending another user message. The
  example's `keywordSearch` returns an empty result when nothing matches, so
  the model can continue.
- Client search runs through `LLMAgent` and `Runner`. Graph LLM nodes report an
  error for a client search.

The full contract is in the
[adapter README](../../model/openai/sdkv3/responses/README.md#tool-search).

## Notes

- `store=false` does not clear the conversation. The in-memory session supplies
  earlier turns, and the adapter sends that history on the next request.
- The example does not run tools itself. Streaming assistant text is displayed
  from deltas. Tool calls are displayed only from a completed
  `Message.ToolCalls` list, and results only from the Runner's committed tool
  message. Partial tool calls, partial tool-result previews, reasoning text,
  and native ProviderData payloads are not displayed as completed tool activity.
  The Runner executes those committed local calls.
- A stream that ends without a valid completed response is an error. Partial
  function calls from that stream are not executed.
- Assistant messages carry native `ProviderData` for the next request. Its
  opaque payload is stored under `provider_data` in `Message` JSON. `Content`
  and `ReasoningContent` are editable display fields; `ToolCalls` controls
  current local execution. AfterModel payload edits regenerate these fields before tools execute;
  projection-only edits leave the native record unchanged.
  Redacting `Content` alone leaves the original text in ProviderData and
  persisted history. The adapter README shows how to read SDK items and edit
  a native function call. Before-tool edits affect execution arguments without
  rewriting ProviderData. Removing ProviderData from an unfinished native tool round does
  not carry a recovery guarantee. Summary replacement, compaction, and
  behavior for other agents are specified in the adapter contract.
- This module does not demonstrate server-managed `previous_response_id` or
  `conversation` continuation, background responses, hosted tools other than
  tool search, or approval flows.
- Between completed turns, selecting another Responses model replays the saved
  native input. Selecting the built-in Chat Completions model sends the visible
  conversation and pairs each tool result with its call by ID. When this
  Runner resumes a pending native tool call with `agent.WithResume(true)`,
  it requires the producing protocol and configured model.
- History, callbacks, model switching, compatibility, recovery, versioning,
  and release limits are defined in the
  [adapter README](../../model/openai/sdkv3/responses/README.md).
  The user guide is the OpenAI Responses API section of
  [docs/mkdocs/en/model.md](../../docs/mkdocs/en/model.md#openai-responses-api)
  and
  [docs/mkdocs/zh/model.md](../../docs/mkdocs/zh/model.md#openai-responses-api).
