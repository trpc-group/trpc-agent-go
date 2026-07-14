# Code Review Agent Example

This example is a deterministic first version of the automatic code review
agent described in issue #2004. It parses a unified diff, applies Go-focused
rules, records permission decisions and sandbox runs, stores the result in
SQLite, and writes `review_report.json` plus `review_report.md`.

The default path is `rule-only` and does not require a model API key.

## Run

From the `examples` module:

```bash
go run ./code_review_agent \
  --fixture security_secret \
  --mode rule-only \
  --out-dir /tmp/code-review-agent \
  --db /tmp/code-review-agent/review.db
```

Run all fixtures:

```bash
go run ./code_review_agent \
  --fixture all \
  --mode rule-only \
  --out-dir /tmp/code-review-agent-fixtures \
  --db /tmp/code-review-agent-fixtures/review.db
```

Review local working tree changes:

```bash
go run ./code_review_agent \
  --repo-path /path/to/repo \
  --sandbox mock \
  --dry-run
```

Review an explicit file list:

```bash
go run ./code_review_agent \
  --repo-path /path/to/repo \
  --files internal/foo.go,internal/bar.go \
  --out-dir /tmp/code-review-agent-files \
  --db /tmp/code-review-agent-files/review.db
```

Query a persisted task:

```bash
go run ./code_review_agent \
  --db /tmp/code-review-agent/review.db \
  --task-id cr-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
```

Use `--sandbox managed` to attempt checks through `codeexecutor/sandbox`.
Use `--sandbox container` for Docker-backed `codeexecutor/container`, and
`--sandbox e2b` for `codeexecutor/e2b` (requires E2B credentials such as
`E2B_API_KEY`). Use `--sandbox local-dev` only for local development; it is
intentionally not the default production-like path.

## Outputs

- `review_report.json`
- `review_report.md`
- SQLite tables:
  - `review_tasks`
  - `review_findings`
  - `sandbox_runs`
  - `permission_decisions`
  - `review_reports`
  - `artifacts`

## Fixtures

The fixtures cover clean diffs, secret leakage, goroutine/context leakage,
resource lifecycle issues, transaction lifecycle issues, missing tests,
duplicate findings, sandbox failure input, and redaction.
