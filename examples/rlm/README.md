# RLM (Recursive Language Model) Example

Implementation of the RLM paradigm ([arXiv:2512.24601](https://arxiv.org/abs/2512.24601)) — enabling LLMs to process **arbitrarily long** documents through recursive code-driven decomposition.

## How It Works

```
┌─────────────────────────────────────────────────────────┐
│  Root Agent (depth=0)                                   │
│  1. Explore context structure via Starlark REPL         │
│  2. Decompose into chunks → rlm_query_batched()        │
│  3. Aggregate child results → final_answer             │
├─────────────────────────────────────────────────────────┤
│  Child Agent (depth=1)         Child Agent (depth=1)    │
│  • Receives sub-context        • Receives sub-context   │
│  • ReAct loop: code → LLM     • Can recurse deeper     │
│  • Returns analysis result     • Or analyze directly    │
├─────────────────────────────────────────────────────────┤
│  Leaf Agent (depth=N)                                   │
│  • Small context, direct analysis via llm_query()      │
│  • No further recursion                                │
└─────────────────────────────────────────────────────────┘
```

Key properties:
- **ReAct Agent Loop** — each node is an autonomous agent with `execute_code` and `final_answer` tools
- **Starlark REPL** — LLM writes Python-subset code to inspect, slice, and process context
- **Symbolic Recursion** — `rlm_query()` spawns child agents on sub-contexts
- **Parallel Execution** — `rlm_query_batched()` / `llm_query_batched()` for concurrent processing
- **Rate Limiting** — token-bucket rate limiter prevents upstream API overload
- **Guardrails** — limits sub-agent fan-out, direct LLM prompt size, and tool output size

## Prerequisites

- Go 1.21+
- Git (for auto-cloning repos)
- An OpenAI-compatible LLM API

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `OPENAI_API_KEY` | Yes | API key for the LLM service |
| `OPENAI_BASE_URL` | No | Custom API endpoint (default: OpenAI) |
| `MODEL_NAME` | No | Model to use (default: `gpt-4o-mini`) |

## Quick Start

```bash
cd examples/rlm

# Set your API credentials
export OPENAI_API_KEY="your-key"
export OPENAI_BASE_URL="https://your-endpoint/v1"
export MODEL_NAME="your-model"

# Run (auto-clones free-programming-books, analyzes all .md files)
go run ./simple/ 2>rlm-run.log
```

The example automatically:
1. Clones [EbookFoundation/free-programming-books](https://github.com/EbookFoundation/free-programming-books) (shallow, ~5s)
2. Collects all `*.md` files (currently ~2.2 MB, 200+ files)
3. Runs RLM analysis: "Identify all outdated or deprecated content"

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--model` | env `MODEL_NAME` | Override model name |
| `--max-depth` | 5 | Maximum recursion depth |
| `--qpm` | 20 | Rate limit (queries per minute) |

## Architecture

```
simple/
├── main.go        # Entry point: CLI flags, repo cloning, context loading
├── service.go     # HTTP server: /api/llm, /api/rlm endpoints
├── rlm.go         # ReAct agent loop: thought → action → observation
├── repl.go        # Starlark REPL with builtins (context, llm_query, rlm_query, ...)
├── tools.go       # Tool definitions: execute_code, final_answer
├── prompt.go      # Dynamic system prompt generation (COORDINATOR/LEAF roles)
└── ratelimit.go   # Token-bucket rate limiter
```

### Starlark Builtins

Available inside `execute_code`:

| Builtin | Description |
|---------|-------------|
| `context` | The full context string (read-only) |
| `llm_query(prompt)` | Single LLM call, returns string |
| `llm_query_batched(prompts)` | Parallel LLM calls, returns list of strings |
| `rlm_query(query, context, boundary="", stop_condition="")` | Spawn a child RLM agent |
| `rlm_query_batched(queries, contexts)` | Spawn parallel child agents (max 10) |
| `print(...)` | Output to observation |

### Runtime Guardrails

| Limit | Value | Behavior |
|-------|-------|----------|
| Tool output returned to LLM | 8 KiB per stdout/stderr stream | Output is truncated with an explicit notice; full output remains in `rlm-code-dump.log` |
| Direct `llm_query` prompt | 30,000 chars | Oversized prompts return an error asking the agent to split the text |
| Child RLM context | 60,000 chars | Oversized child contexts return an error asking the agent to split the context |
| RLM batch fan-out | 10 child agents | Larger batches return an error asking the agent to split the batch |

## Output Files

After a run, two log files are generated in the working directory:

- `rlm-run.log` — agent execution trace (steps, timing, sub-agent lifecycle)
- `rlm-code-dump.log` — all Starlark code executed and their outputs

## Performance Notes

- **20 QPM default** matches common API rate limits; adjust with `--qpm`
- **Model call timeout = 2 min** per LLM request; recursive HTTP calls can wait up to 30 min
- **Burst = 3** initial tokens to avoid cold-start stampede
- **Exponential backoff** on 429 errors (5s → 10s → 20s → 40s → 60s)
- For a multi-MB document, expect tens of minutes with 20 QPM depending on model behavior and recursion fan-out

## Example Output

```
$ go run ./simple/ 2>rlm-run.log

Recursive Language Model (RLM)
==================================================
Model: deepseek-v3
Max Depth: 5
Rate Limit: 20 QPM
Context: 2255996 chars, 32975 lines
Query: Identify all outdated or deprecated content...
==================================================

... (19 sub-agents spawned across 2 batches, ~15 min) ...

FINAL ANSWER:
--------------------------------------------------
# Outdated/Deprecated Content in the Free Programming Books Collection
(84 findings across 9 categories: deprecated frameworks, dead platforms,
 EOL language versions, defunct tools, etc.)
```
