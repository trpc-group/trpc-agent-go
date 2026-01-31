# Ralph Loop Example

This example demonstrates Ralph Loop mode via `runner.WithRalphLoop` for
`LLMAgent` (Large Language Model Agent).

Ralph Loop is useful when an agent tends to stop early because a Large
Language Model (LLM) may *think* it is done, but the task is not actually
complete.

## What it does

- Configures a `Runner` with Ralph Loop mode.
- Runs an interactive Command Line Interface (CLI) task loop.
- Forces the LLM flow to continue until the assistant outputs a completion
  promise like `<promise>DONE</promise>`.
- Uses a real model provider via the OpenAI-compatible Application Programming
  Interface (API) implementation in `model/openai`.
- Uses `-max-iterations` and `-max-llm-calls` as safety valves.

## How to run

From the repo root:

```bash
cd examples/ralphloop
# OpenAI-compatible API key (works for DeepSeek too).
export OPENAI_API_KEY="your-api-key"

# DeepSeek
go run . -model deepseek-chat -variant deepseek

# OpenAI
go run . -model gpt-4o -variant openai
```

Then type a task and press Enter. Type `/exit` to quit.

## Notes

- If you need objective verification (for example, tests must pass), implement
  a custom `runner.Verifier` and pass it via
  `runner.RalphLoopConfig.Verifiers`.
