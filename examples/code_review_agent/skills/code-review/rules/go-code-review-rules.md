# Go Code Review Rules

## GO-SEC-001 Sensitive Information

Detect hard-coded API keys, access tokens, bearer tokens, passwords, AWS access keys, and GitHub tokens. Evidence must be redacted before storage. Severity is critical.

## GO-SEC-002 SQL Construction

Detect SQL statements assembled with `fmt.Sprintf` or string concatenation near `Query`, `QueryContext`, `Exec`, or `ExecContext`. Recommend parameterized SQL. Severity is high.

## GO-CONC-001 Goroutine Cancellation

New goroutines must have a visible cancellation path. For request-scoped code, pass `context.Context` and select on `ctx.Done()`. Severity is medium.

## GO-CONC-002 time.Tick Leak

`time.Tick` cannot be stopped. Use `time.NewTicker`, `defer ticker.Stop()`, and cancellation. Severity is high.

## GO-CONC-003 Detached Request Context

Request-scoped handlers should not create `context.Background()` or `context.TODO()` for downstream calls. Propagate request context. Severity is medium.

## GO-RES-001 Resource Lifecycle

Files, HTTP response bodies, database handles, and similar resources must be closed on every path. Prefer immediate `defer Close()` after the nil-error check. Severity is high.

## GO-DB-001 Rows Lifecycle

`Query` or `QueryContext` rows must be closed and checked with `rows.Err()`. Severity is high.

## GO-DB-002 Transaction Lifecycle

Transactions started with `Begin` or `BeginTx` must have `Rollback` and `Commit` paths. Severity is high.

## GO-ERR-001 Error Handling

Errors from I/O, DB, JSON, conversion, and network calls must not be assigned to `_` unless there is an explicit justification. Severity is medium.

## GO-TEST-001 Test Coverage

Production Go behavior changes that add functions without a corresponding `_test.go` change are sent to human review as low-confidence guidance, not a blocking finding.
