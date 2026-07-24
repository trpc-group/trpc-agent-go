# Go Code Review Rules

This catalog is intentionally deterministic so review fixtures and
hidden tests can run without a model API key. The Go implementation uses
line rules plus AST-assisted hunk parsing where possible.

## go/security/secret-literal

Detects hard-coded values assigned to names such as `apiKey`, `token`,
`secret`, `password`, bearer Authorization headers, JWT-like strings,
AWS access key patterns, OpenAI-style `sk-` tokens, high-entropy opaque
string literals, and private key blocks.

Severity: critical. Evidence must be redacted.

## go/concurrency/goroutine-context

Detects newly added goroutines that do not show a visible lifecycle
signal such as a caller context, `ctx.Done()`, or an equivalent bounded
shutdown path in the surrounding hunk. The hunk-level pass also flags
long-running goroutine loops without cancellation and goroutines that
may block forever on unbuffered channel sends.

Severity: high.

## go/resource/missing-close

Detects opened files, HTTP responses, network connections, SQL handles,
and similar resources when the surrounding hunk has no visible close.
The AST pass also flags lower-confidence wrapper calls with names such
as `openReport`, `Dial*`, or `Create*`, plus result variables with
closable names such as `file`, `conn`, `handle`, `stream`, or `body`,
when their returned value is not closed. Conditional-only close paths
are treated as needing review.

Severity: high.

## go/resource/http-body-close

Detects `http.Client.Do` or equivalent request execution when the hunk
does not visibly close `resp.Body`. This covers the common hidden-sample
shape where the call is not `http.Get` or `http.Post`.

Severity: high.

## go/error/ignored-error

Detects common ignored error forms such as assigning an error result to
`_`, bare `return err` without added operation context, and obvious
unchecked high-risk calls such as SQL `Exec` / `Query`, HTTP requests,
or filesystem removals. Intentional ignores should include a comment
explaining why the operation is safe.

Severity: medium.

## go/db/transaction-lifecycle

Detects added transaction starts without visible commit or rollback in
the hunk. Prefer `defer tx.Rollback()` after a successful begin, then
commit after all operations succeed. Rollback paths hidden behind
conditional branches are flagged because early returns can still leak
the transaction.

Severity: high.

## go/db/rows-close

Detects SQL `Query` or `QueryContext` calls that return rows without a
visible `rows.Close()`. Reviewers should also check for `rows.Err()`
after iteration. Conditional-only `rows.Close()` calls are treated as
insufficient.

Severity: high.

## go/security/sql-concat

Detects SQL query execution that appears to build SQL with string
concatenation or `fmt.Sprintf`, including the common two-line form where
the query string is built before `db.QueryContext`. Parameter
placeholders are still preferred even when only part of the query is
dynamic. The AST pass also tracks simple query helper functions and
`strings.Builder` based construction when the result flows into
`Query`, `Exec`, or `QueryRow` calls. It also tracks simple selector
flows such as `spec.SQL` and helper methods named like SQL/query
builders when they receive dynamic arguments.

Severity: high.

## go/security/dynamic-exec-command

Detects `exec.Command` and `exec.CommandContext` calls with dynamic
command names or dynamically composed arguments. Fixed literal commands
with literal arguments are allowed, including simple variables assigned
to string literals in the reviewed hunk.

Severity: high.

## go/concurrency/mutex-missing-unlock

Detects newly added mutex locks when the surrounding hunk has no visible
unlock path. Prefer `defer mu.Unlock()` immediately after a successful
lock.

Severity: high.

## go/concurrency/shared-mutation

Detects new goroutines that mutate maps, slices, counters, or other
shared state without visible synchronization. This is intentionally
reported with moderate confidence because some state may still be
thread-confined by surrounding code outside the diff. Visible mutex,
atomic, channel, or context synchronization in the same hunk suppresses
the shared-mutation warning.

Severity: high.

## go/resource/defer-in-loop

Detects `defer` statements in loop hunks. Defers inside loops may delay
cleanup until the function returns rather than the end of each
iteration.

Severity: medium.

## go/context/missing-cancel

Detects derived contexts created with `context.WithCancel`,
`context.WithTimeout`, or `context.WithDeadline` when the surrounding
hunk does not call the returned cancel function. Missing cancel calls can
leak timers and child context resources.

Severity: medium.

## go/context/background-in-production

Detects production code that introduces `context.Background()`. Request
and worker paths should normally accept a caller context so cancellation
and deadlines propagate.

Severity: medium, bucket: needs_human_review when confidence is below
the high-confidence threshold.

## go/error/panic-in-goroutine

Detects `panic()` in hunks that launch goroutines. A panic escaping from
a goroutine can terminate the whole process unless it is recovered at
the goroutine boundary.

Severity: critical.

## go/test/missing-test-change

Detects production Go file changes without any changed `*_test.go`
file. This is lower confidence because documentation, generated code,
or pure wiring changes can be valid exceptions.

Severity: low, bucket: needs_human_review.

## sandbox/check-failed

Records failed, denied, timed out, or unavailable sandbox checks as a
human-review item instead of crashing the review task.

Severity: medium, bucket: needs_human_review.

## sandbox/go/diagnostic

Parses `go test` and `go vet` output that contains `file.go:line`
locations into structured findings.

Severity: medium.

## sandbox/staticcheck/*

Parses staticcheck output such as `file.go:line:col: message (SAxxxx)`
into structured findings with the staticcheck rule id preserved.

Severity: medium.
