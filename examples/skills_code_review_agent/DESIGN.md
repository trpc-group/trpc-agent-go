# Design

The prototype separates deterministic review from evidence-producing
execution. The `code-review` Skill contains the workflow, rule reference, and a
bounded diff-summary script. At startup the orchestrator loads the Skill through
`skill.NewFSRepository`, verifies its rule document, and stages the complete
Skill into a per-task workspace. Unified diff parsing remains in Go so file and
hunk boundaries, changed-line numbers, package names, and input limits are
available before any command runs. Rules focus on Go security, goroutine and
context ownership, resource closure, transaction and connection lifecycle,
error handling, and missing tests. This deterministic path also serves as the
fake-model mode.

Production defaults to `codeexecutor/container`. The image has no network,
drops Linux capabilities, enables no-new-privileges, uses a read-only root,
and receives bounded tmpfs, memory, CPU, and PID resources. Each command has a
45-second timeout, clean environment, fixed environment allowlist, and 64 KiB
persisted output limit. Repository snapshots exclude VCS metadata, editor
state, credential files, symlinks, and generated reports, while enforcing file
and total size limits. Local execution is rejected unless the caller opts in
with `--allow-local`.

Every command is converted to a `tool.PermissionRequest` before execution. The
policy allows only the bundled diff script, `go test`, `go vet`, and optional
`staticcheck`. Destructive, network, host-control, compound-shell, and
metacharacter-bearing commands are denied. Unknown commands return `ask`; deny
and ask decisions never reach the workspace runner. Decisions and blocked runs
are retained for audit.

`ReviewStore` is the backend boundary, with SQLite as the default
implementation. The schema separates tasks, redacted input summaries, sandbox
runs, governance decisions, findings, artifacts, reports, and metrics. Writes
use one transaction, and `GetReview` reconstructs a task by ID. Reports and all
database fields pass through credential redaction; the raw diff is represented
only by a SHA-256 digest, byte count, changed-file list, and redacted preview.

Findings carry severity, category, file, line, evidence, recommendation,
confidence, source, and rule ID. Deduplication keeps the strongest result for
each file, changed line, and category. Confidence below 0.80 is excluded from
confirmed findings and placed in warnings and human review. Metrics record
total and sandbox duration, tool calls, permission blocks, finding and warning
counts, severity distribution, and exception types. Sandbox failure changes
task status but never aborts rule analysis, report generation, or persistence.
