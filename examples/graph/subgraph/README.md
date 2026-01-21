# Subgraph Delegation (Parent → Child GraphAgent)

This example demonstrates composing a parent graph that delegates to a child
GraphAgent via a Subgraph node (sugar over an Agent node). It shows how to:

- Build a child graph (LLM + Tools) and expose it as a sub‑agent
- Register the sub‑agent on the parent GraphAgent and delegate to it
- Pass selected runtime state into the subgraph (input mapper)
- Customize how child results are written back (output mapper)
- Isolate child from parent session history, or scope child events
- Stream and forward child events with optional verbose model/tool metadata

## Overview

Parent graph (3 nodes):

```
parse_input → assistant (subgraph) → collect
```

Child subgraph (LLM + tools):

```
llm_decider ↔ tools  (LLM decides when to call tools, then summarizes)
```

- Tool: `schedule_meeting(title, when)` — simulates scheduling; if `when` is
  not provided, it reads `parsed_time` from parent‑injected runtime state.
- Parent `parse_input` extracts a simple time hint from user text and writes
  `parsed_time` into state; `collect` aggregates child outputs into a final
  payload for display.

## Subgraph Features Demonstrated

- Input mapping: only pass specific keys to the child (`parsed_time`) or pass all
- Output mapping: capture `child_last` and `child_final` (child final state)
- Message isolation: run child with `include_contents=none` to ignore parent
  session history for that sub‑run
- Event scoping: set a scope segment so child events are grouped in viewers

## Usage

### Run (flags)

```bash
cd examples/graph/subgraph
# Model defaults to deepseek-chat if unset
# Provide OpenAI‑compatible endpoint and key as needed

go run . \
  -model ${MODEL_NAME:-deepseek-chat} \
  -base-url "$OPENAI_BASE_URL" \
  -api-key  "$OPENAI_API_KEY"
```

Or via environment (flags optional):

```bash
export OPENAI_BASE_URL=https://api.deepseek.com
export OPENAI_API_KEY=sk-...
cd examples/graph/subgraph
go run .
```

Then type messages interactively. Built‑in commands:

- `help` — show help
- `samples` — print sample prompts
- `include none|filtered|all` — override parent message seeding mode for future runs
- `exit` or `quit` — leave

### Example prompts

- `schedule a meeting tomorrow at 3pm titled team sync`
- `hello there`

### Example output (abridged)

```
🧩 Subgraph Demo (Parent calls Child GraphAgent)
Model: deepseek-chat
================================================
✅ Ready. Session: sess-...
Try:
  schedule a meeting tomorrow at 3pm titled team sync
  hello there
> schedule a meeting tomorrow at 3pm titled team sync
...streaming assistant text…
---
[parent/assistant] Scheduled meeting: ...
[final] {"last_response":"...","parsed_time":"2025-...","meeting":{...},"child_last":"...","child_final_keys":N}
```

## Flags

- `-model` — OpenAI‑compatible model name (default: `deepseek-chat`)
- `-base-url` — OpenAI‑compatible base URL
- `-api-key` — API key
- `-v` — verbose: print model/tool metadata and filter segments
- `-parent-include` — seed mode for parent session messages: `none|filtered|all`
- `-sub-isolate` — run child with `include_contents=none` (ignore parent history)
- `-sub-scope` — event scope segment for child (groups events visually)
- `-sub-input` — subgraph input mapping: `parsed|all`
- `-sub-output` — subgraph output mapping: `custom|default`

Notes:
- At runtime you can also type `include none|filtered|all` to update the parent
  include mode for subsequent runs.

## Requirements

- Go 1.21+
- Network access to an OpenAI‑compatible endpoint
- Valid API key (via `-api-key` or env `OPENAI_API_KEY`)

## Files

- `examples/graph/subgraph/main.go` — parent/child graphs, flags, interactive loop

## See Also

- `examples/graph/io_conventions` — detailed I/O conventions for LLM/Agent nodes
- `examples/graph/a2asubagent` — sub‑agent over A2A transport
- `examples/graph/multiturn` — basic multi‑turn graph chat with tools
