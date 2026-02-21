# Anthropic Skills Benchmark for trpc-agent-go

This benchmark is a focused sanity-check for trpc-agent-go **Agent Skills**
compatibility against the public `anthropics/skills` repository.

It exercises:
- Loading a real skills repository (local path or URL archive)
- Running `skill_run` in a workspace (read-only skill tree + writable outputs)
- Installing Python deps inside the workspace via `.venv/`
- Using an LLM to verify `skill_load` / docs injection behavior

## Directory Structure

```
benchmark/anthropic_skills/
├── README.md
├── skills_cache/               # cache for URL-based skills roots
├── skill_workspaces/           # local workspace roots for skill_run
└── trpc-agent-go-impl/         # runnable benchmark program
    ├── go.mod
    └── *.go
```

## Quickstart

```bash
cd benchmark/anthropic_skills/trpc-agent-go-impl
go run .
```

By default the program downloads `anthropics/skills` as a GitHub ZIP
archive and caches it under `../skills_cache/`.

The first run can take a while (downloads, Python installs, and optional
LLM calls). Progress is printed to stderr, and stdout prints `PASS` only
at the end.

By default the agent suite prints step-by-step tool calls and tool
results (similar to `benchmark/gaia`). Use `-debug` to disable log
truncation and include extra debug fields.

## LLM Setup

The agent suite requires a model endpoint via `OPENAI_API_KEY` (and
optionally `OPENAI_BASE_URL`).

```bash
export OPENAI_API_KEY="..."
export OPENAI_BASE_URL="https://api.openai.com/v1"  # optional
export MODEL_NAME="gpt-5"                           # optional
```

If you see a clang "missing sysroot" error (macOS), run Go with
`CGO_ENABLED=0` or `unset SDKROOT` before `go run`.

## Common Flags

- `-skills-root`:
  - Local directory, or a URL to a ZIP/TAR archive
  - Default: `https://github.com/anthropics/skills/archive/refs/heads/main.zip`
- `-progress`: print progress to stderr (default: true)
- `-skills-cache-dir`: cache dir for URL roots (default: `../skills_cache`)
- `-work-root`: local workspace root (default: `../skill_workspaces`)
- `-suite`:
  - `tool`: deterministic `skill_run` checks only
  - `agent`: LLM-based checks (`skill_load` + `skill_list_docs`)
  - `all`: run both (default)
  - `token-report`: compare token usage between progressive disclosure and full injection
  - `prompt-cache`: compare prompt-cache usage with skills injected into tool results vs system prompt
- `-model`: model name for agent suite (default: `$MODEL_NAME` or `gpt-5`)
- `-with-exec`: run extra exec cases (default: true)
- `-only-skill`: run a single skill name (optional)
- `-debug`: include tool/answer details on failures
- `-token-report-all-docs`: for `token-report`, preload all docs (default: true)

## Fast Smoke Runs

```bash
cd benchmark/anthropic_skills/trpc-agent-go-impl
go run . -suite tool -with-exec=false
go run . -suite agent -with-exec=false -only-skill webapp-testing
```

## Token Report

The token report suite measures **actual** model token usage for the
same composed scenario under two modes:
- Mode A: progressive disclosure (overview only; skills loaded on demand)
- Mode B: full injection (preload all skills into context)

It prints token usage from model responses (`usage.prompt_tokens`,
`usage.completion_tokens`, `usage.total_tokens`) aggregated across all
model calls.

```bash
cd benchmark/anthropic_skills/trpc-agent-go-impl
go run . -suite token-report -model gpt-5
```

If your model cannot fit all skill docs in its context window, run the
same report with docs disabled:

```bash
go run . -suite token-report -token-report-all-docs=false
```

### What “with skills” vs “without skills” means

This benchmark focuses on **progressive disclosure**:
- **With skills (Mode A)**: inject only the low-cost overview first, and
  load skill bodies/docs only when needed.
- **Without skills (Mode B)**: simulate “inline everything” by forcing
  the framework to inject all skill content up-front (all `SKILL.md`
  bodies, and optionally all docs).

This is the practical baseline most people hit when they don’t have
progressive disclosure and just paste a whole skills repo into the
prompt.

### Example results

Model: `gpt-5`.

Scenario: `brand_landing_page` (uses `brand-guidelines` + `frontend-design`).

Full injection = all skills + all docs (`-token-report-all-docs=true`):
| Mode | Prompt | Completion | Total | Prompt savings |
| --- | ---: | ---: | ---: | ---: |
| A: progressive disclosure | 41975 | 4725 | 46700 | 95.04% |
| B: full injection | 846713 | 2373 | 849086 | - |

Full injection = all skills (SKILL.md only, no docs)
(`-token-report-all-docs=false`):
| Mode | Prompt | Completion | Total | Prompt savings |
| --- | ---: | ---: | ---: | ---: |
| A: progressive disclosure | 38627 | 2440 | 41067 | 83.79% |
| B: full injection | 238302 | 5822 | 244124 | - |

Notes:
- These numbers vary by model/provider and by which scenario you run.
- The main savings come from **prompt tokens** (input size), because
  progressive disclosure prevents large skills/docs from being inlined
  unless the agent actually needs them.

## Prompt Cache Report

The prompt cache report suite compares two progressive-disclosure modes:
- **Mode A (legacy):** loaded skill bodies/docs are appended to the system prompt
- **Mode B (tool results):** loaded skill bodies/docs are materialized into tool
  result messages (`skill_load` / `skill_select_docs`)

This matters for providers that support prompt caching because caching typically
works on a **prefix**: if a later request shares an identical prefix with an
earlier request, that prefix can be reused from cache. Injecting loaded skill
content into the system prompt shifts subsequent messages (user/history) and can
reduce the shared prefix. Materializing into tool results keeps the system prompt
more stable and often increases cached prompt tokens in multi-step tool flows.

The suite runs the `internal-comms` case (requires `skill_load`,
`skill_select_docs`, and `skill_run`) in both modes and prints a cache delta.

Reported fields depend on provider:
- OpenAI-style APIs: `usage.prompt_tokens_details.cached_tokens`
- Anthropic-style APIs: `usage.prompt_tokens_details.cache_read_tokens` and
  `usage.prompt_tokens_details.cache_creation_tokens`

```bash
cd benchmark/anthropic_skills/trpc-agent-go-impl
go run . -suite prompt-cache -model gpt-5
```

### Example results

Model: `gpt-5`.

Case: `internal-comms` (runs `skill_load`, `skill_select_docs`, `skill_run`).

| Mode | Steps | Prompt | Cached | Uncached (Prompt - Cached) | Completion | Total | Duration |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| A: system prompt (legacy) | 4 | 19442 | 2432 | 17010 | 481 | 19923 | 16.997s |
| B: tool results | 5 | 25344 | 8832 | 16512 | 1274 | 26618 | 27.024s |

Notes:
- Cache fields are provider-specific. For OpenAI-style APIs, `Cached` is
  `usage.prompt_tokens_details.cached_tokens`.
- A useful derived metric is **uncached prompt tokens**:
  `prompt_tokens - cached_tokens`. In this run, Mode B increased total prompt
  tokens (more context materialized into tool results), but also increased cache
  hits enough to slightly reduce uncached prompt tokens overall.
- These numbers can vary by provider/model and can be non-deterministic.

## Notes

- The tool suite verifies that the staged skill tree is read-only while
  `out/`, `work/`, `inputs/`, and `.venv/` remain usable.
