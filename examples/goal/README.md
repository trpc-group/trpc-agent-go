# Goal Extension Example

This example demonstrates the `agent/extension/goal` LLMAgent extension.

The extension keeps using ordinary `Runner.Run`. It does not add a separate
goal entrypoint and it does not emit multiple runner completions. While a
session goal is active, the LLMAgent extension blocks premature final model
responses inside the same model loop until the model calls `update_goal` with
`complete` or a genuinely terminal `blocked` status, or the retry budget is
exhausted.

## Quick start

```bash
cd trpc-agent-go/examples
export OPENAI_API_KEY="<your-key>"
export OPENAI_BASE_URL="https://api.openai.com/v1" # optional
go run ./goal -model deepseek-v4-flash
```

By default the example leaves temperature unset so models with fixed sampling
settings can use their provider defaults. Use `-temperature 0.3` only when the
selected model supports it.

Try a manual goal:

```text
/goal Draft a release checklist, verify risks, and produce a final summary.
```

You can also try a normal message that asks for cross-turn persistence:

```text
Keep working until you have a concrete migration plan and mark it complete only after the plan is ready.
```

The model may call `create_goal` for the second form when it decides the user is
asking for a session goal.

## What to notice

- `/goal <objective>` is parsed by this example, not by the framework core.
- The extension contributes `get_goal`, `create_goal`, and `update_goal` to the LLMAgent.
- Session goal state is persisted in session state and restored by the extension on later turns.
- Premature final answers are blocked by the extension's `AfterModel` callback, so the model keeps looping inside the same `Runner.Run`.
- Streaming is controlled by the example's `-streaming` flag and is not changed by the Goal extension.
- The outer caller still sees one `runner.completion`.
- `token_budget` is intentionally not part of this example.

## Options shown

```go
llmAgent := llmagent.New(
    "goal-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithExtensions(goal.New(
        goal.WithMaxRetries(3),
    )),
)

r := runner.NewRunner(
    "goal-extension-demo",
    llmAgent,
    runner.WithSessionService(sessionService),
)
```
