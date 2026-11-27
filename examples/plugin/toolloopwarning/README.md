# Tool Loop Warning Plugin Example

This example shows how to register `plugin/toolloopwarning` on a `Runner` and
observe a request-local warning after two identical, complete tool rounds.

It uses a deterministic scripted model so the repeated rounds occur on every
run without an API key. The example still uses the normal `Runner`, `LLMAgent`,
function-tool, plugin, and session paths.

## What this example demonstrates

- Install the opt-in plugin with `runner.WithPlugins(...)`.
- Detect semantically identical JSON arguments despite key order and
  whitespace differences.
- Use tool-call IDs to pair results with calls while excluding the IDs from
  the round fingerprint.
- Add the warning to the third model request after two complete repeated
  rounds.
- Let the model change its trajectory after seeing the warning; the plugin
  itself does not stop or retry the run.
- Keep the warning out of persisted session events.

## Run

```bash
cd examples
go run ./plugin/toolloopwarning
```

No API key or network access is required.

## Expected output

```text
model request 1: loop warning=false
tool search_docs: query="trpc-agent-go" limit=3
model request 2: loop warning=false
tool search_docs: query="trpc-agent-go" limit=3
model request 3: loop warning=true
assistant: The repeated tool loop was detected, so I stopped calling the tool.
tool calls: 2
session contains loop warning: false
```

## Core integration

Register the plugin when constructing the Runner:

```go
runnerInstance := runner.NewRunner(
	"my-app",
	agentInstance,
	runner.WithPlugins(toolloopwarning.New(
		toolloopwarning.WithWarningMessage(loopWarning),
	)),
)
```

`WithWarningMessage` is optional. Calling `toolloopwarning.New()` without
options uses the default warning. Polling tools and other tools whose repeated
results are expected can be excluded with `WithExcludedToolNames(...)`.

## Why the model is scripted

A provider-backed model cannot be expected to produce two exactly matching
tool rounds on demand. The scripted model varies both the call ID and JSON
formatting between its first two tool calls while keeping their semantics and
tool results equal. On the third request it verifies that the warning is
present and returns a final answer instead of calling the tool again.

The scripted model exists only to make the trigger deterministic. It does not
bypass the plugin or invoke its callbacks directly.
