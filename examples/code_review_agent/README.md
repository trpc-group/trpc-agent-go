# Code Review Agent Example

This example shows a deterministic Go code review agent built around a
`code-review` Skill, governed sandbox commands, SQLite persistence, and
structured JSON and Markdown reports. It is intentionally scoped to the example
module and does not add public framework APIs.

## Run

From the `examples` module:

```bash
go run ./code_review_agent --fixture clean --dry-run
```

The default output directory is `./output`. Each review writes
`<output-dir>/<task-id>/review_report.json`,
`<output-dir>/<task-id>/review_report.md`, and a SQLite database at
`<output-dir>/reviews.db`.

When `--output-dir` is set without `--db-path`, the database follows the output
directory automatically. An explicit `--db-path` always takes precedence.

## Inputs

Use exactly one review input:

```bash
go run ./code_review_agent --fixture secret_leak --dry-run
go run ./code_review_agent --diff-file ./change.diff --runtime fake
go run ./code_review_agent --repo-path ../ --files pkg/a.go --files pkg/b.go --runtime fake
```

`--repo-path` runs `git diff HEAD -- <files>` through a fixed argument array.
The `--files` values must be relative paths and cannot escape the repository.

## Runtime Modes

The production default is `--runtime=e2b`. Use `--dry-run` for the fully local
fake runtime; it exercises parsing, rule evaluation, governance, sandbox run
records, SQLite writes, and report rendering without external credentials.

```bash
go run ./code_review_agent --fixture command_injection --dry-run
```

Use local execution only for development:

```bash
go run ./code_review_agent --repo-path ../ --runtime local --allow-local
```

`--rule-only` disables advisory model behavior. The current example does not
call a real model provider, so findings come from deterministic diff and Go rule
checks plus sandbox diagnostics. `--enable-staticcheck` adds an optional
staticcheck sandbox command.

For E2B, set `E2B_API_KEY` and provide a template that already has Go, Bash,
and a Bash Jupyter kernel:

```bash
go run ./code_review_agent \
  --repo-path ../ \
  --runtime e2b \
  --e2b-template go-review-template
```

The template value can also be provided through `TRPC_AGENT_CODE_REVIEW_E2B_TEMPLATE`.

## SQLite And CGO

SQLite storage uses `github.com/mattn/go-sqlite3`, which requires CGO and a C
compiler at runtime. Non-CGO builds still compile, but opening a SQLite store
returns a clear error. Use `--db-path` to select the database location.

Query a stored task:

```bash
go run ./code_review_agent --show-task review-123 --db-path ./reviews.db
```

`--show-task` rebuilds the canonical JSON report from normalized SQLite tables;
it does not require the report files to still exist.

## Design Notes

The agent is a governed, deterministic code review orchestrator rather than a
free-form LLM reviewer. A filesystem Skill named `code-review` defines the
review workflow, rule catalog, and sandbox script entrypoint, while the Go
example owns all command selection. Inputs are normalized from a unified diff,
fixture, or read-only `git diff` invocation, then parsed into changed files,
hunks, candidate new lines, and package metadata. Deterministic rules inspect
added lines and available Go context for hardcoded secrets, command injection,
disabled TLS verification, goroutine and context leaks, unclosed files, HTTP
bodies, SQL rows, ignored errors, database handle and transaction lifecycle
issues, and missing tests. Confidence is fixed by rule type and context quality:
high-confidence matches become findings, lower-confidence inferences become
warnings that require human review. Dedupe uses file, line, and category, keeping
the strongest result and counting suppressed matches.

Sandbox execution is advisory evidence. The agent builds private command specs
from a closed enum, validates them through a command gate, then calls a
`tool.PermissionPolicy` before any execution. Denied or ask decisions are stored
and reported without invoking the runner. Allowed commands run with clean
environment settings, timeout and output limits, and restricted artifacts. E2B is
the production-style runtime; fake mode is deterministic for tests; local mode is
an explicit development fallback. All evidence, sandbox output, governance
reasons, reports, and stored fields pass through redaction before persistence.
SQLite stores a review task, diff summary, decisions, sandbox runs, findings,
warnings, metrics, artifacts, and final report metadata, but not the raw diff.
The normalized schema deliberately separates repeated child records from the
task row so that another SQL backend can implement the same private store
contract without changing the report model. Report files are restricted to the
two declared artifact kinds and are written with owner-only permissions.

The diff parser treats Git-quoted paths as structured tokens, including paths
with spaces and C-style escaped UTF-8 bytes. Rules use line-oriented evidence
where that is deterministic, while syntax-sensitive checks such as shell
execution inspect a small Go AST to distinguish a fixed literal command from a
payload assembled from variables or concatenation. Missing-test warnings are
matched to the changed file's directory instead of being suppressed by an
unrelated test elsewhere in the patch. These choices keep dry-run behavior
stable while reducing the false positives and false negatives that matter for
hidden evaluation samples.

## Test Data

The public fixtures live in `testdata/fixtures.json`. Example sanitized report
outputs are checked in as:

- `testdata/review_report.json`
- `testdata/review_report.md`
