# Code Review Rules

Rule catalog for the **code-review** Skill. IDs align with `internal/rules` (deterministic engine) and LLM supplemental findings (`source=llm`).

## Rule IDs

| Rule ID | Category | Severity | Trigger (added lines in diff) |
|---------|----------|----------|--------------------------------|
| SEC-001 | security | high | SQL built with `+` or `fmt.Sprintf` |
| SEC-002 | security | high | `exec.Command(...)` with string concatenation |
| CONC-001 | concurrency | high | `go func()` without cancel / `ctx.Done()` in hunk |
| CONC-002 | concurrency | medium | `go func()` while hunk has `context.Context` but goroutine ignores `ctx` |
| RES-001 | resource | high | `os.Open` / `sql.Open` without `defer ...Close()` in hunk |
| DB-001 | resource | high | `.Begin()` without `.Commit()` / `.Rollback()` in hunk |
| ERR-001 | error_handling | medium | `_ = err` or empty `if err != nil {}` |
| SENS-001 | sensitive_data | critical | Hardcoded password / API key / token / secret, or secrets in logs |
| TEST-001 | testing | low | New exported `func` without matching `*_test.go` in the change set |

## Finding shape

Each issue uses: `severity`, `category`, `file`, `line`, `title`, `evidence`, `recommendation`, `confidence`, `source`, `rule_id`.

| Source | Meaning |
|--------|---------|
| `rule` | Deterministic regex rules on parsed diff (`--dry-run=true` default) |
| `llm` | Supplemental findings from Agent + Skill when `--dry-run=false` |

## Dedup and confidence

- **Dedup key:** `file:line:category` — duplicates are dropped.
- **Confirmed findings:** `confidence >= 0.6`
- **Needs human review:** `confidence < 0.6` → warnings section in the report (not mixed into high-confidence findings).

## Sandbox checks (Phase 2)

Scripts complement regex rules; failures are recorded but do not abort the review task.

| Check | Command | Notes |
|-------|---------|-------|
| Diff validation | `bash scripts/run_checks.sh work/inputs/changes.diff` | Unified diff format, line limit (5000), ignored-error signal (exit 2) |
| Static analysis | `go vet ./...` | Workspace `work/repo/` when `--repo-path` is set |
| Tests | `go test ./...` | Same as above when repo is staged |

**Runtime:** production → `--runtime=container` (image `golang:1.24-bookworm`) or `--runtime=e2b` (`E2B_API_KEY`). `--runtime=local` is dev fallback only.

**PermissionPolicy:** high-risk commands (`rm -rf`, `curl\|bash`, `git push`) are **denied** before sandbox execution. Allowed: skill scripts, `go vet`, `go test`.

## Agent workflow (Phase 3)

When `--dry-run=false`:

1. Rule engine runs first (`source=rule`).
2. Agent loads this skill via `skill_load`, may call `skill_run` for scripts above.
3. LLM returns additional findings as JSON (`source=llm`); merged and deduped with rule output.

Redaction runs before reports and database writes (API keys, tokens, passwords).
