# Skills Code Review Agent

Automatic Go code review agent prototype built on tRPC-Agent-Go patterns. Combines deterministic diff rules, Agent Skill scripts, PermissionPolicy gating, sandbox checks, SQLite persistence, and JSON/Markdown reports.

## Requirements

- Go 1.23+
- CGO enabled (SQLite via `github.com/mattn/go-sqlite3`)
- Optional: Docker for `--runtime=container` production sandbox

## Quick Start

```bash
cd examples/skills_code_review_agent
go run . --fixture 02_security --dry-run
```

Reports: `output/review_report.json`, `output/review_report.md`  
Database: `reviews.db`

## CLI Flags

| Flag | Description |
|------|-------------|
| `--fixture` | Fixture name under `fixtures/` (without `.diff`) |
| `--diff-file` | Path to a unified diff file (mutually exclusive with `--fixture` / `--repo-path`) |
| `--repo-path` | Git repo path (`git diff HEAD` for final working tree; excludes untracked files) |
| `--dry-run` | Rule-only mode without LLM (default: `true`) |
| `--fake-model` | Agent path with mock model, no API key (tests full Agent orchestration) |
| `--model` | OpenAI model when `--dry-run=false` (default: `gpt-4o-mini`) |
| `--runtime` | Sandbox: `local` (dev fallback), `container` (prod, purpose-built Go+python3 image), `e2b`, `skip` |
| `--skip-sandbox` | Disable sandbox execution |
| `--skills-root` | Skills directory (default: `skills`) |
| `--db-path` | SQLite path (default: `reviews.db`) |
| `--output-dir` | Report output directory (default: `output`) |

## Architecture

```
Input (diff / repo / fixture)
  → diff parser → rules engine → dedup / partition
  → PermissionPolicy gate → sandbox (skill_run / go vet / go test)
  → redact → report + SQLite
```

- **Skill**: `skills/code-review/SKILL.md` + `docs/rules.md` + `scripts/run_checks.sh`
- **Sandbox**: `internal/sandbox` with timeout, output cap, env whitelist
- **Storage**: `review_tasks`, `findings`, `review_metrics`, `artifacts`, `sandbox_runs`, `permission_decisions`

## Fixtures (8 samples)

| Fixture | Scenario |
|---------|----------|
| `01_clean` | Harmless refactor, no findings |
| `02_security` | SQL injection + hardcoded API key |
| `03_goroutine_leak` | Goroutine without cancellation |
| `04_resource_leak` | File opened without deferred close |
| `05_db_connection` | Transaction without commit/rollback |
| `06_missing_test` | Exported function without test changes |
| `07_duplicate_finding` | Duplicate security findings deduplicated |
| `08_sandbox_fail` | Ignored error + sandbox check failure (task still completes) |

Run all:

```bash
for f in 01_clean 02_security 03_goroutine_leak 04_resource_leak 05_db_connection 06_missing_test 07_duplicate_finding 08_sandbox_fail; do
  go run . --fixture "$f" --dry-run --output-dir "output/$f"
done
```

## Tests

```bash
go test ./...
```

Covers: diff parsing, rule matching, dedup, redaction, sandbox permission/failure, SQLite round-trip, end-to-end pipeline.

## Production Sandbox

Use **container** or **e2b** runtime in production (`local` is dev fallback only).

Container uses a purpose-built image from `docker/Dockerfile` (Go + bash + python3):

```bash
go run . --repo-path /path/to/go/module --runtime=container --dry-run
```

Network-isolated container checks require a `vendor/` tree (or staged module cache). When dependencies are unavailable, `go vet` / `go test` are skipped with `deps_unavailable` instead of crashing the review.

E2B cloud sandbox (requires `E2B_API_KEY`):

```bash
export E2B_API_KEY=e2b_...
go run . --fixture 02_security --runtime=e2b --dry-run
```

High-risk commands (`rm -rf`, `curl|bash`, `git push`) are denied by PermissionPolicy before sandbox execution.

## LLM / Fake Model Modes

Rule-only (default, no API key):

```bash
go run . --fixture 02_security --dry-run
```

Fake model (Agent + Skill orchestration, no API key):

```bash
go run . --fixture 01_clean --fake-model
```

Real LLM:

```bash
export OPENAI_API_KEY=sk-...
go run . --fixture 02_security --dry-run=false --model gpt-4o-mini
```

## Deliverables

- Go example + CLI (`main.go`)
- `skills/code-review/` Skill, rules doc, scripts
- SQLite schema + store interface
- 8 fixture diffs + sample reports under `output/`
- [DESIGN.md](DESIGN.md) — architecture and security boundaries
