# Review rules

The executable declarations live in `../rules/rules.json`. This document defines
what constitutes evidence and what must be excluded. Rules inspect added lines
and, when available, local AST/data-flow context related to those lines.

## Confidence and reporting

- High-confidence, directly attributable evidence enters `findings`.
- Ambiguous patch-only or incomplete lifecycle evidence enters `warnings` or
  `needs_human_review`; it is never promoted solely because its severity is high.
- Findings are normalized and deduplicated by cleaned file, line, and category.
- Generated and vendored files, non-secret comment-only matches, obvious secret
  examples/placeholders, and unrelated pre-existing lines are excluded.
- Evidence and recommendations are redacted before normalization, persistence,
  reporting, logging, or telemetry.

## Rule catalog

### `GO-SECRET-001` — sensitive information

Detect provider keys, bearer/JWT values, passwords, private-key material, and
credential-bearing DSNs introduced by the change. Report only the redacted
evidence. Exclude obvious placeholders, comment examples, environment lookups,
and already-redacted tokens; test files are not categorically trusted.

### `GO-SEC-001` — security

Detect shell execution and dangerous file permissions added by the change.
Require a recognized shell invocation or an unsafe literal mode. Exclude fixed
non-shell commands and safe permission constants.

### `GO-CTX-001` — context lifecycle

Detect `context.WithCancel`, `WithTimeout`, or `WithDeadline` without a same-scope
cancel call, and tickers without `Stop`. Exclude immediate same-scope cleanup.

### `GO-GOR-001` — goroutine lifecycle

Detect newly launched goroutines without an observable termination mechanism,
such as context cancellation, a closed channel, or bounded completion. Because
patches may omit surrounding ownership, uncertain cases require human review.

### `GO-RES-001` — resource lifecycle

Detect files, HTTP response bodies, SQL rows, and similar closers that are opened
without cleanup. Exclude a same-scope explicit close or defer placed after a
successful acquisition and error check.

### `GO-ERR-001` — error handling

Detect discarded typed errors and unchecked calls to known error-returning APIs.
Exclude intentional handling with an explanatory contract, APIs with no error
return, and test assertions that already verify the failure.

### `GO-DB-001` — database lifecycle

Detect transactions without rollback fallback, rows without close, and `sql.Open`
inside the reviewed scope. Injected long-lived pools are not treated as newly
opened handles.

### `GO-TEST-001` — missing tests

Detect behavior-changing Go production edits without a same-package test change.
Exclude documentation, generated code, test-only edits, and comment/package/import
only changes. This rule is advisory when the diff lacks complete package context.

## Sandbox checks

`go-test` and `go-vet` are fixed checks executed only by the trusted runner after
Filter, Permission, and durable decision recording over exact runtime values.
Their failure or timeout is execution evidence, not a source finding: it is
stored in the sandbox summary and marks the review incomplete.
