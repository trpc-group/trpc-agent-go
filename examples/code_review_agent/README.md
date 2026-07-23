# Code Review Agent Example

Deterministic Go code-review pipeline on tRPC-Agent-Go: Skills, real
`codeexecutor` sandboxes, Permission governance, SQLite persistence, and
optional fake-model Agent orchestration.

See [DESIGN.md](./DESIGN.md).

## Quick start

```bash
cd examples/code_review_agent
go test ./...

# Local demo (recommended without Docker)
go run . --fixture goroutine_leak --executor local --mode rule-only --out ./out

# Production default: container (fails hard without Docker unless fallback enabled)
go run . --fixture secret_leak --executor container --out ./out
# or explicitly allow fallback:
go run . --fixture secret_leak --executor container --allow-local-fallback --out ./out

# File-list input
go run . --files path/a.go,path/b.go --executor local --out ./out
go run . --files @/tmp/files.txt --executor local --out ./out

# Fake-model LLM assist (no API key; findings still from the rule engine)
go run . --fixture clean --executor local --mode llm --llm fake --out ./out

# Real OpenAI-compatible LLM assist
export OPENAI_API_KEY=sk-xxxx
# optional for DeepSeek / other gateways:
# export OPENAI_BASE_URL=https://api.deepseek.com
go run . --fixture goroutine_leak --executor local --mode llm --llm openai \
  --model gpt-4o-mini --out ./out
```

## Real LLM usage

`--mode=llm` enables the Skills agent assist path. Findings remain **rule-engine
authoritative**; the model only orchestrates `skill_load` / `workspace_exec`.

| `--llm` | Behavior |
|---------|----------|
| `fake` (default) | Scripted local model, no network |
| `openai` | Real OpenAI-compatible chat API |
| `auto` | Use `openai` when `OPENAI_API_KEY` is set, else `fake` |

Environment:

```bash
export OPENAI_API_KEY=sk-xxxx           # required for --llm=openai
export OPENAI_BASE_URL=https://...      # optional OpenAI-compatible endpoint
```

Examples:

```bash
# OpenAI
export OPENAI_API_KEY=sk-xxxx
go run . --diff-file ./change.patch --executor local --mode llm --llm openai \
  --model gpt-4o-mini --out ./out

# DeepSeek (OpenAI-compatible)
export OPENAI_API_KEY=sk-xxxx
export OPENAI_BASE_URL=https://api.deepseek.com
go run . --fixture security_injection --executor local --mode llm --llm openai \
  --model deepseek-chat --model-variant deepseek --out ./out

# Qwen / DashScope (OpenAI-compatible)
export OPENAI_API_KEY=sk-xxxx   # or DASHSCOPE_API_KEY
export OPENAI_BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1
go run . --fixture security_injection --executor local --mode llm --llm openai \
  --model qwen-flash --model-variant qwen --out ./out

# Auto: real if key present, otherwise fake
go run . --fixture clean --executor local --mode llm --llm auto --out ./out
```

`--model-variant` must be a framework variant (`openai|deepseek|qwen|...`), not the model
id. `qwen-flash` as a variant is wrong; use `--model qwen-flash --model-variant qwen`.
`OPENAI_BASE_API` is accepted as an alias of `OPENAI_BASE_URL`.

Runtime outputs under `./out/` are gitignored. Committed sample reports live in
`testdata/sample_output/` (portable paths, no secrets).

## CLI

| Flag | Default | Description |
|------|---------|-------------|
| `--diff-file` | | unified diff / patch |
| `--repo-path` | | git working tree |
| `--files` | | comma-separated paths or `@listfile` |
| `--fixture` | | name under `testdata/fixtures` |
| `--executor` | `container` | `container\|e2b\|local\|fake` |
| `--allow-local-fallback` | `false` | fall back when container/e2b unavailable |
| `--mode` | `rule-only` | `rule-only\|dry-run\|llm` |
| `--llm` | `fake` | `fake\|openai\|auto` (only with `--mode=llm`) |
| `--model` | `gpt-4o-mini` | model name for openai/auto |
| `--base-url` | | OpenAI-compatible base URL (`OPENAI_BASE_URL`) |
| `--model-variant` | | optional `openai\|deepseek\|...` |
| `--enable-go-test` | `false` | schedule `run_go_vet.sh` |
| `--enable-staticcheck` | `false` | schedule `run_staticcheck.sh` (skips if missing) |
| `--db` / `--out` | `./out/...` | persistence + reports |
| `--confidence-threshold` | `0.75` | findings threshold |

Governance demo commands (`curl`, broad `go test ./...`) are injected for `--fixture` runs only, so real reviews are not polluted with intentional deny/ask probes. Persist/store errors fail the run instead of being ignored.

## Fixtures

Public fixtures include `expected.json` and are checked by `go test ./orchestrator`.
Hidden eval set: `go test ./eval` (detection ≥80%, FP ≤15%).

## Skill scripts

`skills/code-review/scripts/`: `run_checks.sh`, `run_go_vet.sh`, `run_go_test.sh`, `run_staticcheck.sh`.

## Tests

```bash
go test ./...
```
