# Code Review Report

Task `review-7ed8cd6d5ee2` finished with status `failed`.

## Summary

Model plan "mock-model" coordinated skill "code-review" for 9 changed files, produced 18 findings, and recorded 4 sandbox runs.

## Findings Summary

| Severity | Count |
| --- | ---: |
| critical | 2 |
| high | 3 |
| medium | 11 |
| low | 2 |

| Category | Count |
| --- | ---: |
| concurrency | 1 |
| database | 1 |
| error | 2 |
| resource | 1 |
| security | 4 |
| test | 9 |

## Model Plan

- model: mock-model
- provider: mock
- source: mock_planner
- skill: code-review
- runtime: fake
- commands: go test ./..., go vet ./..., go test ./skills/code-review/scripts, go test ./internal/rules
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

## Fix Recommendations

- `test.missing_coverage`: Add or update tests covering the new behavior.
- `security.redaction_required`: Verify all persisted reports and audit records contain only redacted values.
- `security.secret_leak`: Move secrets to a managed secret store and rotate the exposed credential.
- `db.lifecycle`: Ensure every successful transaction commits and every failed path rolls back.
- `error.ignored_error`: Handle, return, or explicitly document why the error is safe to ignore.
- `resource.close_missing`: Defer Close after checking the open/query error.
- `concurrency.goroutine_context_leak`: Thread context into the goroutine and exit on cancellation.

## Human Review

- `test.missing_coverage` pkg/config.go:1 status=warning confidence=0.72 title=New Go file has no related test change
- `test.missing_coverage` pkg/db.go:1 status=warning confidence=0.72 title=New Go file has no related test change
- `test.missing_coverage` pkg/dup.go:1 status=warning confidence=0.72 title=New Go file has no related test change
- `test.missing_coverage` pkg/fail.go:1 status=warning confidence=0.72 title=New Go file has no related test change
- `test.missing_coverage` pkg/file.go:1 status=warning confidence=0.72 title=New Go file has no related test change
- `test.missing_coverage` pkg/hello.go:1 status=warning confidence=0.72 title=New Go file has no related test change
- `test.missing_coverage` pkg/new_logic.go:1 status=warning confidence=0.72 title=New Go file has no related test change
- `test.missing_coverage` pkg/redact.go:1 status=warning confidence=0.72 title=New Go file has no related test change
- `test.missing_coverage` pkg/worker.go:1 status=warning confidence=0.72 title=New Go file has no related test change
- `concurrency.goroutine_context_leak` pkg/worker.go:4 status=needs_human_review confidence=0.78 title=Goroutine lacks visible context cancellation

## Governance

Blocked or escalated decisions: 0.

- `workspace_exec` action=allow safety=allow risk=low blocked=false reason=Command is allowed by the example policy.
- `workspace_exec` action=allow safety=allow risk=low blocked=false reason=Command is allowed by the example policy.
- `workspace_exec` action=allow safety=allow risk=low blocked=false reason=Command is allowed by the example policy.
- `workspace_exec` action=allow safety=allow risk=low blocked=false reason=Command is allowed by the example policy.

## Sandbox

Sandbox duration: 0 ms. Output is redacted and capped.

- `go test ./...` runtime=fake status=passed exit=0 error= truncated=false
- `go vet ./...` runtime=fake status=passed exit=0 error= truncated=false
- `go test ./skills/code-review/scripts` runtime=fake status=passed exit=0 error= truncated=false
- `go test ./internal/rules` runtime=fake status=passed exit=0 error= truncated=false

## Metrics

- findings: 18
- permission blocks: 0
- redactions: 2
- total duration ms: 0
- sandbox duration ms: 0
- tool calls: 4
- severity distribution: {"critical":2,"high":3,"low":2,"medium":11}
- error distribution: {}

Conclusion: needs_human_review
