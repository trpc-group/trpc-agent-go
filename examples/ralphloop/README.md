# Ralph Loop Example

This example demonstrates `planner/ralphloop`, a planner-based Ralph Loop mode
for `LLMAgent` (Large Language Model Agent).

Ralph Loop is useful when an agent tends to stop early because a Large
Language Model (LLM) may *think* it is done, but the task is not actually
complete.

## What it does

- Configures an `LLMAgent` with a RalphLoop Planner.
- Forces the LLM flow to continue until the assistant outputs
  `<promise>DONE</promise>`.
- Uses a real model provider (DeepSeek via OpenAI-compatible API).
- Uses `MaxIterations` and `WithMaxLLMCalls` as safety valves.

## How to run

From the repo root:

```bash
cd examples/ralphloop
# DeepSeek API key (recommended).
export DEEPSEEK_API_KEY="..."
go run .
```

## Notes

- If you need objective verification (for example, tests must pass), implement
  a custom `ralphloop.Verifier` and pass it via `ralphloop.Config.Verifiers`.
