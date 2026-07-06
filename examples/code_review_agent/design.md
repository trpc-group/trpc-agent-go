# Code Review Agent Design

The example is structured as a narrow orchestration layer around deterministic
internal packages:

- `internal/diffparse` parses unified diffs into changed files, hunks, added
  line numbers, and Go package directories.
- `internal/rules` applies deterministic review rules for secrets,
  goroutine/context leaks, resource closing, ignored errors, missing tests, and
  database lifecycle issues.
- `internal/redact` redacts suspected secrets before any value is stored or
  rendered.
- `internal/safetywrap` records framework permission action, original safety
  decision, risk level, rule id, reason, and blocked status for each planned
  command.
- `internal/sandboxrun` provides a fake-testable sandbox execution seam with
  timeout, truncation, failure, and unavailable-runtime records.
- `internal/orchestrator` coordinates model planning, Skill rules, safety
  decisions, sandbox runs, persistence, and report rendering. The default
  planner calls an OpenAI-compatible chat completions endpoint for non-fake
  runtimes while keeping unit tests deterministic through the fake runtime.
- `internal/store` defines a storage interface and dependency-free durable
  implementation for tasks, inputs, sandbox runs, permission decisions,
  findings, artifacts, and reports. The entrypoint keeps the `NewSQLite` name
  for the example default while using an equivalent JSON-backed `.db` file to
  avoid CGO and high-Go-version driver dependencies in examples.
- `internal/report` renders JSON and Markdown reports from the persisted review
  result, including the non-secret model plan used for the run.

The Skill under `skills/code-review` owns the audit policy and rule guidance.
The Go implementation keeps deterministic parsing, redaction, deduplication,
and persistence independently testable so unit tests can run without model keys
or remote sandboxes.
