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

The `report_paths` fields, artifact paths, and stdout summary use
output-directory-relative identifiers such as
`<task-id>/review_report.json`. Resolve a report file by joining the configured
`<output-dir>` with that identifier. The absolute or configured output
directory is never embedded into those persistent path fields.

When `--output-dir` is set without `--db-path`, the database follows the output
directory automatically. An explicit `--db-path` always takes precedence.

## Inputs

Use exactly one review input:

```bash
go run ./code_review_agent --fixture secret_leak --dry-run
go run ./code_review_agent --diff-file ./change.diff --runtime fake
go run ./code_review_agent --repo-path ../ --files pkg/a.go --files pkg/b.go --runtime fake
```

`--repo-path` may name the worktree root or a directory below it. The agent first
resolves `git rev-parse --show-toplevel`, then runs `git diff HEAD -- <files>`
from that canonical root through a fixed argument array. Git execution disables
external diff, textconv, fsmonitor, pagers, inherited `GIT_*` configuration,
and interactive prompting before repository configuration is consulted. The
`--files` values are repository-root-relative literal Git paths and cannot
escape the repository. A scoped review does not upload files outside that
scope, so repository test, vet, and staticcheck commands are skipped with a
human-review warning instead of running against an incomplete Go module.

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

Local execution uses a fresh temporary work root for every sandbox command, but
it is not an operating-system security boundary: reviewed code still runs as
the current host user. Use it only with trusted development inputs. Production
E2B repository reviews create an independent sandbox for `go version`,
`go test`, `go vet`, and optional staticcheck, so enabling staticcheck creates
four sandboxes instead of three.

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
The value is a filesystem path, not a SQLite DSN: `file:` URIs, `:memory:`, NUL
bytes, and `?` query parameters are rejected. The accepted path is resolved to
one absolute path before file checks and SQLite opening; the agent adds fixed
foreign-key and DELETE-journal settings internally and uses a single database
connection.
New database files are created with owner-only permissions. On POSIX systems,
existing database and SQLite sidecar files are tightened to `0600`; symlinks,
non-regular files, and group- or other-writable parent directories are rejected.
New output directories use `0700`. Windows retains the file-type checks but
does not claim POSIX mode-bit enforcement.

Query a stored task:

```bash
go run ./code_review_agent --show-task review-123 --db-path ./reviews.db
```

`--show-task` rebuilds the canonical JSON report from normalized SQLite tables;
it does not require the report files to still exist. Tasks are checkpointed as
`running` before input is read and end as `completed` or `failed`. The report's
`stage` and optional redacted `failure` identify where a failure occurred, so
input, governance, report-write, and persistence failures remain queryable.
SQLite schema v2 adds those lifecycle fields and automatically migrates schema
v1 databases while preserving existing task and child rows.

## Design Notes

The agent is a governed, deterministic code review orchestrator rather than a
free-form LLM reviewer. A trusted, embedded Skill named `code-review` defines
the review workflow, rule catalog, and sandbox script entrypoint, while the Go
example owns all command selection. Each review materializes the verified Skill
into a private temporary directory and removes it when the review finishes;
working-directory Skill overrides are not loaded. Inputs are normalized from a
unified diff, fixture, or read-only `git diff` invocation, then parsed into
changed files, hunks, candidate new lines, and package metadata. Deterministic
rules inspect added lines and available Go context for hardcoded secrets,
command injection, disabled TLS verification, goroutine and context leaks,
unclosed files, HTTP bodies, SQL rows, ignored errors, database handle and
transaction lifecycle issues, and missing tests. Confidence is fixed by rule
type and context quality: high-confidence matches become findings,
lower-confidence inferences become warnings that require human review. Dedupe
uses file, line, and category, keeping the strongest result and counting
suppressed matches.

Sandbox execution is advisory evidence. The agent builds private command specs
from a closed enum, validates them through a command gate, then calls a
`tool.PermissionPolicy` before creating or invoking the command's runner. Denied
or ask decisions are stored and reported without creating a sandbox. Every real
repository-dependent command receives a fresh copy of the same host snapshot in
an independent E2B sandbox or local temporary work root, so reviewed tests cannot
rewrite the source later analyzers inspect. Allowed commands run with clean
environment settings, timeout and output limits, and restricted artifacts. E2B
is the production-style runtime; fake mode is deterministic for tests; local
mode is an explicit development fallback rather than a hostile-code boundary.
Complete repository snapshots resolve changed Go files to their nearest
`go.mod` and run checks once per affected module. The snapshot root contains a
reserved `.trpc-agent-review-modules` manifest with
sorted, repository-relative module directories separated by NUL bytes; `.` names
the root module. Snapshot enumeration and copying share a 30-second deadline and
enforce fixed limits for tracked entries, unique directories, path bytes, and
actual copied content. A timeout or limit failure skips repository-dependent
checks and produces a human-review warning. All evidence, sandbox output,
governance reasons, reports, and stored fields pass through redaction before
persistence.
Standard Go diagnostics emitted by test, vet, and staticcheck are parsed from
stdout and stderr. A diagnostic becomes a high-confidence finding only when its
path uniquely maps to a changed file and its line is inside a new-side hunk;
ambiguous, out-of-hunk, truncated, or otherwise incomplete output retains a
governance warning for human review. Synthetic skipped runs remain visible but
do not count as runner tool calls.
SQLite stores a review task, diff summary, decisions, sandbox runs, findings,
warnings, metrics, artifacts, and final report metadata, but not the raw diff.
The normalized schema deliberately separates repeated child records from the
task row so that another SQL backend can implement the same private store
contract without changing the report model. Report files are restricted to the
two declared artifact kinds and are written with owner-only permissions.

The diff parser treats Git-quoted paths as structured tokens, including paths
with spaces and C-style escaped UTF-8 bytes. Hunk ranges must use valid,
non-overflowing line numbers and remain ordered and non-overlapping on both
sides. New, deleted, rename, `/dev/null`, and mode metadata must agree; valid
Git binary patches may omit text path markers. Hunk counts are validated when a
new hunk or file starts and at end of input. Any malformed, impossible, or
incomplete diff requires human review and cannot receive a pass conclusion.
Rules use line-oriented evidence where that is deterministic, while syntax-sensitive
checks such as shell execution inspect a small Go AST to distinguish a fixed
literal command from a payload assembled from variables or concatenation.
Missing-test warnings are matched to the changed file's directory instead of
being suppressed by an unrelated test elsewhere in the patch. These choices
keep dry-run behavior stable while reducing the false positives and false
negatives that matter for hidden evaluation samples.

## Test Data

The public fixtures live in `testdata/fixtures.json`. Example sanitized report
outputs are checked in as:

Fixture JSON is parsed strictly. Every fixture declares expected finding and
warning rule IDs, and optional fake sandbox results for go version, test, vet,
and staticcheck. Missing command results use deterministic successful defaults;
declared results are used exactly. Unknown fields, trailing JSON, invalid run
values, and empty or duplicate expected rule IDs are rejected.

- `testdata/review_report.json`
- `testdata/review_report.md`
