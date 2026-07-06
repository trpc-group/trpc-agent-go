# Code Review Report

Task `review-7ed8cd6d5ee2` finished with status `failed`.

## Summary

Model plan "mock-model" coordinated skill "code-review" for 9 changed files, produced 18 findings, and recorded 1 sandbox runs.

## Model Plan

- model: mock-model
- provider: mock
- source: mock_planner
- skill: code-review
- runtime: fake
- commands: go test ./...
- rules: skills/code-review/SKILL.md, skills/code-review/docs/rules.md

## Findings

- **medium** `test.missing_coverage` pkg/config.go:1 - New Go file has no related test change
  Evidence: `config.go`
  Recommendation: Add or update tests covering the new behavior.
- **low** `security.redaction_required` pkg/config.go:4 - Secret-like value was redacted
  Evidence: `return "[REDACTED_SECRET]"`
  Recommendation: Verify all persisted reports and audit records contain only redacted values.
- **critical** `security.secret_leak` pkg/config.go:4 - Potential secret committed in diff
  Evidence: `return "[REDACTED_SECRET]"`
  Recommendation: Move secrets to a managed secret store and rotate the exposed credential.
- **medium** `test.missing_coverage` pkg/db.go:1 - New Go file has no related test change
  Evidence: `db.go`
  Recommendation: Add or update tests covering the new behavior.
- **high** `db.lifecycle` pkg/db.go:6 - Transaction lacks commit or rollback handling
  Evidence: `tx, err := db.Begin()`
  Recommendation: Ensure every successful transaction commits and every failed path rolls back.
- **medium** `test.missing_coverage` pkg/dup.go:1 - New Go file has no related test change
  Evidence: `dup.go`
  Recommendation: Add or update tests covering the new behavior.
- **medium** `error.ignored_error` pkg/dup.go:4 - Error value is ignored
  Evidence: `_ = err`
  Recommendation: Handle, return, or explicitly document why the error is safe to ignore.
- **medium** `error.ignored_error` pkg/dup.go:5 - Error value is ignored
  Evidence: `_ = err`
  Recommendation: Handle, return, or explicitly document why the error is safe to ignore.
- **medium** `test.missing_coverage` pkg/fail.go:1 - New Go file has no related test change
  Evidence: `fail.go`
  Recommendation: Add or update tests covering the new behavior.
- **medium** `test.missing_coverage` pkg/file.go:1 - New Go file has no related test change
  Evidence: `file.go`
  Recommendation: Add or update tests covering the new behavior.
- **high** `resource.close_missing` pkg/file.go:6 - Opened resource is not closed nearby
  Evidence: `f, err := os.Open("data.txt")`
  Recommendation: Defer Close after checking the open/query error.
- **medium** `test.missing_coverage` pkg/hello.go:1 - New Go file has no related test change
  Evidence: `hello.go`
  Recommendation: Add or update tests covering the new behavior.
- **medium** `test.missing_coverage` pkg/new_logic.go:1 - New Go file has no related test change
  Evidence: `new_logic.go`
  Recommendation: Add or update tests covering the new behavior.
- **medium** `test.missing_coverage` pkg/redact.go:1 - New Go file has no related test change
  Evidence: `redact.go`
  Recommendation: Add or update tests covering the new behavior.
- **low** `security.redaction_required` pkg/redact.go:4 - Secret-like value was redacted
  Evidence: `return "[REDACTED_SECRET]"`
  Recommendation: Verify all persisted reports and audit records contain only redacted values.
- **critical** `security.secret_leak` pkg/redact.go:4 - Potential secret committed in diff
  Evidence: `return "[REDACTED_SECRET]"`
  Recommendation: Move secrets to a managed secret store and rotate the exposed credential.
- **medium** `test.missing_coverage` pkg/worker.go:1 - New Go file has no related test change
  Evidence: `worker.go`
  Recommendation: Add or update tests covering the new behavior.
- **high** `concurrency.goroutine_context_leak` pkg/worker.go:4 - Goroutine lacks visible context cancellation
  Evidence: `go func() {`
  Recommendation: Thread context into the goroutine and exit on cancellation.

## Governance

- `workspace_exec` action=allow safety=allow risk=low blocked=false reason=Command is allowed by the example policy.

## Sandbox

- `go test ./...` runtime=fake status=passed exit=0 error=

## Metrics

- findings: 18
- permission blocks: 0
- redactions: 2

Conclusion: needs_human_review
