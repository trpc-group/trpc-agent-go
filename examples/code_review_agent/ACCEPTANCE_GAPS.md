# Code Review Agent Acceptance Status

This document tracks the remaining acceptance status for
`examples/code_review_agent` after the implementation batches.

## Implemented

- `skills/code-review/SKILL.md`, rule docs, and tested Go script helpers.
- Input modes: `--fixture-dir`, `--diff-file`, `--repo-path`, and `--file-list`.
- Unified diff parsing with file, hunk, candidate line, and Go package metadata.
- Deterministic rules for security, goroutine/context lifecycle, resources,
  errors, missing tests, redaction, and database transaction lifecycle.
- Structured findings with severity, category, file, line, title, evidence,
  recommendation, confidence, source, rule id, status, and fingerprint.
- Deduplication by file, line, category, and rule id plus warning /
  needs-human-review routing for lower-confidence issues.
- OpenAI-compatible planner integration for non-fake runs.
- Permission/safety decisions before sandbox initialization or execution.
- Real `codeexecutor/container`, `codeexecutor/e2b`, and development-only
  `local` workspace runtime adapters; `fake` remains deterministic for tests.
- Per-command sandbox timeout, output cap, redacted stdout/stderr, environment
  allowlist, and runtime initialization failure records.
- JSON-backed `.db` durable store through a swappable `Store` interface plus
  `internal/store/schema.sql` for SQLite-compatible storage.
- JSON and Markdown reports with findings summary, severity/category stats,
  human-review items, governance interceptions, sandbox summary, metrics,
  error distribution, and executable recommendations.

## Verification

```powershell
cd E:\trpc-agent-go\examples\code_review_agent
$env:GOPROXY='https://goproxy.cn,direct'
$env:GOSUMDB='sum.golang.google.cn'
go test ./...
```

## Residual Acceptance Notes

- Hidden-sample thresholds such as high-risk recall >= 80%, false-positive rate
  <= 15%, and redaction recall >= 95% require the hidden benchmark set. The
  implementation provides deterministic fixtures and rule coverage, but the
  hidden-data metric cannot be proven from this repository alone.
- The default durable backend is JSON-backed for dependency-free example runs.
  `internal/store/schema.sql` defines the equivalent SQLite schema so a strict
  SQL backend can be swapped behind `Store` without changing orchestration.
- Generated local outputs under `examples/code_review_agent/out/` remain
  untracked run artifacts unless a release package explicitly wants refreshed
  sample reports.
