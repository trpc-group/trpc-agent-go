# Sandboxed Go code review agent

This example implements an auditable, deterministic code-review pipeline for Go changes. It loads a repository-native `code-review` Skill, parses only changed lines, runs focused rules, asks a `tool.PermissionPolicy` before every command, executes approved checks in an isolated workspace, persists normalized records in SQLite, and publishes JSON plus Markdown reports.

The rule engine does not require a model key. `--dry-run` and `--fake-model` exercise input parsing, Skill loading, governance, deduplication, redaction, persistence, monitoring, and report publication without Docker or an external model.

## Quick start

From this directory:

```bash
go run . --fixture secret --dry-run --output-dir output --db output/reviews.sqlite
```

The command prints its task ID and the exact paths to:

```text
output/<task-id>/report/review_report.json
output/<task-id>/report/review_report.md
output/<task-id>/diff_stats.json
```

Use `--task-id` when a stable identifier is useful for database queries. Task
IDs are immutable: replaying the same input requires a new task ID, while an
existing task is replayed by querying its stored report.

## Inputs

Exactly one primary input is required:

```bash
go run . --diff-file change.diff --dry-run
go run . --repo-path /path/to/repo --executor container
go run . --repo-path /path/to/repo --file-list changed-files.txt --dry-run
go run . --fixture resource --dry-run
```

`--repo-path` reads the working-tree diff relative to `HEAD`. A file list is newline-delimited and resolved beneath its repository root; absolute paths, traversal, and symlink escapes are rejected.

## Executors and safety

`container` is the production default. The container backend disables network access, drops Linux capabilities, enables `no-new-privileges`, uses a read-only root filesystem, and limits memory, CPU, PIDs, and writable tmpfs storage through `codeexecutor/container`. `e2b` is available explicitly for remote sandbox use. `local` is an unsafe development fallback and is rejected unless `--allow-local-fallback` is present. `fake` is selected automatically by `--dry-run`.

Before execution, the repository is copied into a bounded snapshot containing only Go sources and module/workspace files. Hidden directories, `.git`, vendor trees, node modules, symlinks, environment files, private keys, and unrelated artifacts never enter the workspace. Commands receive a clean environment with a small allowlist, a deadline, and bounded stdout/stderr. Artifacts are allowlisted by the application and limited to 1 MiB.

The Skill rule table controls which deterministic rules are enabled. Its
audited statistics script runs in the sandbox, and the agent collects and
cross-checks the generated JSON against the parsed diff before accepting the
run. The Permission policy allows only:

- `go test ./...`
- `go vet ./...`
- `staticcheck ./...`
- the audited `skills/code-review/scripts/diff_stats.sh` invocation

Unknown commands become `ask`; malformed or injected approved commands become `deny`. Neither disposition executes.

## Findings and storage

Every finding contains `severity`, `category`, `file`, `line`, `title`, `evidence`, `recommendation`, `confidence`, `source`, `rule_id`, and a stable fingerprint. The engine covers security, command/SQL injection, goroutine/context lifetime, resource closure, transaction rollback, ignored/swallowed errors, and missing tests. Observations are deduplicated by file, line, and category. Lower-confidence test coverage observations are kept under `needs_human_review`.

SQLite contains separate tables for tasks, input summaries, sandbox runs, permission decisions, findings, artifacts, monitoring metrics, and final reports. Writes use one transaction and the pipeline verifies that the report can be queried by task ID before returning success. Raw diffs are not stored. Credential-like values are redacted before findings, logs, reports, or database payloads are created.

## Fixture matrix

The repository includes more than the eight required cases: clean diff, hard-coded secret, goroutine leak, context cancellation leak, resource close, transaction lifecycle, ignored error, missing test, duplicate issue, SQL injection, and sandbox failure.

```bash
./scripts/run_fixtures.sh
```

Windows PowerShell:

```powershell
.\scripts\run_fixtures.ps1
```

## Verification

```bash
go test ./...
go vet ./...
go test -count=1 -cover ./internal/review
```

Core tests cover diff line mapping, file-list traversal, rule fixtures, clean-diff false positives, deduplication, credential redaction, Permission decisions, sandbox failure recovery, safe repository snapshots, SQLite round trips, and report sections.

The current core-package coverage is `85.3% of statements`; the command above
is the source of truth and coverage must remain at or above 85%.

## Framework integration

The example adds bounded stdout/stderr retention to workspace program runs and
context-aware initialization for container and E2B executors. These shared
executor changes let the review pipeline enforce its output and startup
deadlines consistently. `MaxOutputBytes` is also honored by local interactive
sessions; non-positive values retain the runtime default behavior.

See [DESIGN.md](DESIGN.md) for the 300–500 Chinese-character design summary and `sample_output/` for checked-in report examples.
