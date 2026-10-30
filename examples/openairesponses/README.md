# OpenAI Responses API examples

## Purpose

This independent module shows how to use the OpenAI Responses adapter with
`LLMAgent` and `Runner`. The adapter currently pins OpenAI SDK `v3.74.0`.

- `chat` is a multi-turn conversation without tools.
- `tools` registers a local calculator and keeps one in-memory session across
  user messages.

Every request sends the full client-managed conversation. The adapter defaults
to `store=false`, and both programs set that explicitly. In ordinary Runner
use, application code does not read or edit `Message.Replay`.

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
| `-model` | Model name passed to `responses.New`. | `gpt-5` |
| `-streaming` | Stream assistant text. | `true` |

`go run ./chat -help` and `go run ./tools -help` print those flags and do not
call the API.

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

## Core API

```go
llm := responses.New(modelName, responses.WithStore(false))
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

## Notes

- `store=false` does not clear the conversation. The in-memory session supplies
  earlier turns, and the adapter sends that history on the next request.
- The example does not run tools itself. Streaming assistant text is displayed
  from deltas. Tool calls are displayed only from a completed
  `Message.ToolCalls` list, and results only from the Runner's committed tool
  message. Partial tool calls, partial tool-result previews, reasoning text,
  and native Replay payloads are not displayed as completed tool activity.
  The Runner executes those committed local calls.
- A stream that ends without a valid completed response is an error. Partial
  function calls from that stream are not executed.
- Assistant messages carry a native replay record. Do not edit that projection
  in place. Summary replacement, compaction, and behavior for other agents are
  specified in the adapter contract.
- This module does not demonstrate server-managed `previous_response_id` or
  `conversation` continuation, background responses, hosted tools, tool search,
  or approval flows.
- History, callbacks, model switching, compatibility, recovery, versioning,
  and release limits are defined in the
  [adapter README](../../model/openai/sdkv3/responses/README.md).
  The user guide is the OpenAI Responses API section of
  [docs/mkdocs/en/model.md](../../docs/mkdocs/en/model.md#openai-responses-api)
  and
  [docs/mkdocs/zh/model.md](../../docs/mkdocs/zh/model.md#openai-responses-api).
