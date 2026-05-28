# Context Compaction Example

This example demonstrates prompt-side context compaction for large tool
results. It uses a real OpenAI-compatible model and prints the projected model
request by default so you can verify what the model actually receives.

## What It Does

The program runs two turns in the same session:

1. The user asks the model to call `large_log`, a tool that returns a large
   synthetic log payload.
2. The user asks what the previous tool returned.

With context compaction enabled, the current tool result remains visible during
the active tool turn. On the next turn, the historical `large_log` result is
replaced with `Historical tool result omitted to save context.` when it exceeds
`-tool-result-max-tokens`.

## Run

From the `examples` module:

```bash
cd examples
export OPENAI_API_KEY="..."
export MODEL_NAME="gpt-5.2"

go run ./context_compaction
```

Debug request printing is enabled by default:

```bash
go run ./context_compaction -debug=true
```

To reduce output:

```bash
go run ./context_compaction -debug=false
```

## Useful Scenarios

Show historical Pass 1 replacement:

```bash
go run ./context_compaction \
  -model=gpt-5.2 \
  -log-lines=80 \
  -tool-result-max-tokens=40
```

Protect recent tail events from Pass 1:

```bash
go run ./context_compaction \
  -model=gpt-5.2 \
  -log-lines=80 \
  -tool-result-max-tokens=40 \
  -skip-recent-events=3
```

Force-clean historical results from a noisy tool by name:

```bash
go run ./context_compaction \
  -model=gpt-5.2 \
  -force-clean-large-log
```

Enable Pass 2 head+tail truncation for any oversized tool result:

```bash
go run ./context_compaction \
  -model=gpt-5.2 \
  -oversized-tool-result-max-tokens=120
```

## What To Look For

In the debug output, inspect `role=tool` messages:

- During the first tool loop, the current `large_log` result should have a
  large `content_bytes` value.
- On the second user turn, the same historical tool result should become a
  short placeholder unless protected by `-keep-recent-requests` or
  `-skip-recent-events`.

The example lives under the public examples tree:
[examples/context_compaction](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/context_compaction).
