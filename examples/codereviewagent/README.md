# Governed Skills Code Review Agent

This example implements an auditable code-review pipeline for Go diffs. It combines a repository Skill, deterministic rules, tool permission decisions, an optional `codeexecutor/sandbox` validation probe, SQLite persistence, finding deduplication, credential redaction, monitoring, and JSON/Markdown reports. The default rule-only mode requires no model, API key, container, or external service.

## Run

```bash
cd examples/codereviewagent
go run . --diff-file ./fixtures/command-injection.diff
go test ./...
```

Use `--repo-path /path/to/repository` to review the working-tree diff; when set, it takes precedence over `--diff-file` and the default fixture. Set `--dry-run=false` on a supported Linux or macOS host to run the validation probe with managed OS isolation. The sandbox uses a ten-second timeout, 4 KiB output cap, 256 MiB memory budget, restricted networking, a core environment allowlist, and default secret-name exclusion. Failure is recorded as `SAN001` and never crashes the review task.

## Design

The input layer converts a unified diff into file, new-line number, and added-content records. File types route the task to `skills/code-review/SKILL.md`; the Skill defines stable rule IDs and the confidence policy. Deterministic analysis covers credential exposure, command injection, goroutine and context lifecycle, database ownership, closeable resources, and missing tests. Findings carry file range, severity, category, confidence, source, rule ID, status, explanation, and remediation. Values below 0.80 are separated into `needs_human_review`. Deduplication uses file, line, and rule ID so repeated passes cannot inflate the report.

Before sandbox execution, a permission policy only allows fixed `go test` or `go vet` argument vectors. The current pipeline invokes `go test`; `go vet` remains an allowed alternative for future validation configurations. Unknown executables are denied and shell metacharacters require human approval. The managed runner stages a minimal validation module into `codeexecutor/sandbox`; dry-run uses the same governance and persistence path without executing a process.

SQLite is the default audit store. Its schema separates review tasks, sandbox runs, permission decisions, findings, artifacts, and final reports while keeping task-ID queries simple. Only the diff hash is persisted; raw diff text is never stored. Credential-like values and sandbox output are redacted before persistence. Generated report artifacts include SHA-256 digests and byte sizes in the database.

Nine fixtures cover clean code, command injection, goroutine/context leakage, resource leakage, database lifecycle, missing tests, repeated findings, sandbox failure, and secret redaction. Tests verify parsing, deduplication, redaction, database queries, failure containment, and the complete report pipeline. This structure keeps the agent deterministic for CI while leaving model-backed reasoning as an additive source rather than a prerequisite for security or audit guarantees.
