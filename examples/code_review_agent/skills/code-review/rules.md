# Review Rules

This catalog defines deterministic rules emitted by `scripts/check.sh`. The model review stage may add semantic findings, but it must not duplicate these rule IDs for the same file/line/category.

| rule_id | Category | Severity | Status | Trigger | Guardrail / Notes |
|---------|----------|----------|--------|---------|-------------------|
| `secret-leak` | `security` | `critical` | `finding` | Added code contains API keys, bearer tokens, GitHub tokens, JWT-like tokens, private keys, DB URLs with credentials, or long assigned secret-like values. | Placeholder values such as `dummy`, `placeholder`, `changeme`, and short test values should not be critical findings. Evidence must be redacted. |
| `panic-direct` | `error_handling` | `high` | `finding` | Added line calls `panic(`. | Intended for newly introduced non-test failure paths; future refinements may exclude generated code or explicit test panic assertions. |
| `goroutine-leak` | `concurrency` | `high` | `finding` | Added goroutine has no visible lifecycle signal in the hunk. | Suppressed when the hunk includes `WaitGroup`, `ctx.Done`, `errgroup`, `done`, or `sync.`. |
| `context-leak` | `lifecycle` | `high` | `finding` | Added `context.WithCancel`, `WithTimeout`, or `WithDeadline` without visible cancel handling. | Suppressed when the hunk includes `defer cancel()`, `ctx.Done`, or `cancel()`. |
| `resource-leak` | `resource` | `high` | `finding` | Added `os.Open`, `os.OpenFile`, or `os.Create` without visible close handling. | Suppressed when the hunk includes `defer` or `Close()`. |
| `db-lifecycle` | `database` | `high` | `finding` | Added `sql.Open`, `.BeginTx`, or `.Begin(` without visible cleanup. | Suppressed when the hunk includes `Rollback()` or `Close()`. |
| `http-body-close` | `resource` | `high` | `finding` | Added HTTP client call creates a response without visible `Body.Close()`. | Suppressed when the hunk includes `Body.Close()`. |
| `sql-string-concat` | `security` | `critical` | `finding` | Added SQL text is built with string concatenation or `fmt.Sprintf`. | Parameterized queries and placeholders should not trigger. |
| `command-injection` | `security` | `critical` | `finding` | Added `exec.Command` / `exec.CommandContext` uses a shell `-c` path or dynamic argument. | Literal command/argument lists through `exec.CommandContext(ctx, ...)` should not trigger. |
| `context-background-misuse` | `lifecycle` | `medium` | `finding` | Added `context.Background()` inside a hunk that already has a `context.Context` parameter. | Prefer propagating the existing `ctx`. |
| `mutex-unlock-missing` | `concurrency` | `high` | `finding` | Added `.Lock()` has no visible `.Unlock()` in the hunk. | Suppressed when `defer ...Unlock()` appears in the hunk. |
| `defer-in-loop` | `resource` | `medium` | `finding` | Added `defer` appears inside a loop-shaped hunk. | Use helper functions or explicit close before the next iteration. |
| `bare-return-err` | `error_handling` | `medium` | `finding` | Added `return err` without contextual wrapping. | `fmt.Errorf("operation: %w", err)` should not trigger. |
| `string-concat-loop` | `performance` | `low` | `needs_human_review` | Added `+=` string assembly in a loop-shaped hunk. | Low-confidence performance signal; kept out of high-confidence findings. |
| `todo-marker` | `maintainability` | `medium` | `finding` | Added `TODO(` or `FIXME`. | Recommendation should ask for tracked issue or removal before merge. |
| `missing-test-hint` | `testing` | `low` | `warning` | Added function in selected Go files lacks an error return and has no nearby test signal. | Low-confidence advisory only; must stay in warnings, not high-confidence findings. |

## Fixture Coverage

Public fixtures live in `testdata/fixtures/` and can be exact-match evaluated with the
integration test below.

Holdout/adversarial fixtures live in `testdata/holdout/`. They are committed local
acceptance cases; the `model-*` cases are exercised with the deterministic fake provider.

Additional local external fixtures can still be injected with:

```bash
CR_AGENT_RUN_FIXTURE_MATRIX_TEST=1 go test -tags=integration \
  ./cmd/review-agent -run '^TestAllFixturesMatchExpectedReviewResults$'

# Run an external diff through the same CLI path.
go run ./cmd/review-agent --diff-file /path/to/case.diff \
  --runtime local-fallback --output-dir /tmp/cr-report
```
