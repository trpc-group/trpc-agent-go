# Code Review Agent Design

The example implements Issue 2004 as a narrow code-review orchestration layer.
The `skills/code-review` directory owns the Skill contract: `SKILL.md` explains
how to run review, `docs/rules.md` defines review policy, and `scripts/`
contains deterministic helpers for diff summaries and Go rule checks. The Go
entrypoint keeps Skill execution auditable by recording model-planned commands,
rule sources, Permission decisions, sandbox outcomes, findings, artifacts, and
metrics for every task.

Input parsing is centralized in `internal/inputsource`: it accepts fixture
directories, unified diff files, `git diff` from a repo path, and
newline-delimited file lists. `internal/diffparse` converts unified diffs into
changed files, hunks, candidate line numbers, and Go package directories.
`internal/rules` applies deterministic rules for security, goroutine/context
leaks, resource closing, ignored errors, missing tests, and database lifecycle.
Findings are normalized by fingerprint so the same file, line, category, and
rule cannot be reported twice; lower-confidence severe items route to
`needs_human_review` instead of high-confidence findings.

Sandbox isolation is selected through `--runtime`. `container` uses the
`codeexecutor/container` workspace runtime with a Go toolchain image; `e2b`
uses the `codeexecutor/e2b` workspace runtime; `local` is retained only as a
development fallback; `fake` keeps tests deterministic without Docker, E2B, or
API keys. Every planned command first passes `internal/safetywrap`, which
records the framework action, safety decision, risk level, rule id, reason, and
blocked flag. Denied, ask, and needs-human-review decisions are recorded and
skipped before sandbox initialization. Allowed commands run with a per-command
timeout, output-size cap, redacted stdout/stderr, and a strict environment
allowlist.

Persistence is behind `internal/store.Store`. The default `NewSQLite` entrypoint
uses a dependency-free JSON-backed `.db` file for examples, while
`internal/store/schema.sql` documents the equivalent SQLite schema for strict SQL
backends. The schema covers review tasks, input summaries, sandbox runs,
permission decisions, findings, artifacts, and final reports. `internal/report`
renders both JSON and Markdown with severity/category summaries, human-review
items, governance interception summary, sandbox summary, monitoring metrics,
error distributions, and executable fix recommendations.
