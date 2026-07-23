# Code Review Rules

## CR-SEC-001 — Security

Detect SQL string concatenation and `InsecureSkipVerify: true`.

**Fix:** use parameterized queries; never disable TLS verification in production.

## CR-SEC-002 — Secrets

Detect hard-coded API keys, tokens, passwords, AWS access key IDs, Bearer tokens.

**Fix:** remove secrets from source; load from a secret manager.

## CR-CON-001 — Goroutine / Context

Detect `go func(` without a derived context on the same line.

**Fix:** pass `ctx` and ensure the goroutine exits on cancel.

## CR-RES-001 — Resource lifecycle

Detect `os.Open` / HTTP responses / SQL rows opened without `Close`.

**Fix:** `defer` close on all paths.

## CR-DB-001 — DB lifecycle

Detect `sql.Open` without `Close`, or transactions without Commit/Rollback.

**Fix:** close DB handles; always finish transactions.

## CR-ERR-001 — Error handling

Detect discarded call errors (`_ = foo()`) and `panic(` in non-test files.

**Fix:** return or handle errors; avoid panic in library code.

## CR-TEST-001 — Missing tests


Detect changed non-test `.go` files without a corresponding `_test.go` in the diff.

**Fix:** add focused unit tests for the change.
