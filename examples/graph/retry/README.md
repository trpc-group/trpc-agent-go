# Graph Retry/Backoff Example

This example demonstrates node-level retry and backoff in a graph workflow, using a simple "unstable API" node that intentionally fails a few times before succeeding. It shows how to:

- Configure per-node retry policy with exponential backoff and jitter
- Keep node execution atomic: failed attempts do not produce writes
- Observe retry attempts via node events while keeping default streaming enabled

## Features

- Interactive CLI using Runner + GraphAgent
- Default streaming output from LLM node
- User-friendly prompts and verbose mode with retry metadata

## Run

```bash
cd examples/graph/retry
go run .
```

Environment:

- If your LLM provider requires credentials (e.g. `OPENAI_API_KEY`), export it before running.
- Default model is `deepseek-chat` (adjust via `--model`).

## Flags

- `--model string` model name to use (default: `deepseek-chat`)
- `--fail int` number of initial failures for the unstable node (default: 2)
- `--latency duration` simulated latency per attempt (default: `200ms`)
- `--verbose` print detailed node/tool/model and retry info

Example:

```bash
go run . --fail=2 --latency=500ms --verbose
```

## What to look for

- When the "unstable_api" node fails, you will see error events that include:
  - Attempt number and maximum attempts
  - Planned `nextDelay` before retry
  - `retrying=true` for intermediate attempts

- After retries succeed, the downstream LLM node will stream a final answer.

## Code highlights

- `WithRetryPolicy(graph.WithSimpleRetry(3))` on the `unstable_api` node
- Node errors are retried inside the executor; no writes occur until a successful attempt
- Metadata emitted in node error events allows user-friendly retry displays in verbose mode

