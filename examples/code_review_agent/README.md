# Code Review Agent Example

This example is a deterministic first version of the automatic code review
agent described in issue #2004. It parses bounded review inputs, applies
Go-focused rules, records permission decisions and sandbox runs, stores the
result in pure-Go SQLite, and atomically publishes JSON, Markdown, and artifact
manifest files.

The default path is `rule-only` and does not require a model API key.

## Modes

`--mode` selects how the review runs:

- `rule-only` (default): deterministic rule scanning only, no model involved.
- `fake-model`: runs the full agent orchestration (`agent/llmagent` +
  `runner` + in-memory session) with a deterministic offline model. It needs
  no API key and produces the stable `FAKE001` finding, which makes the whole
  chain (prompt building, event streaming, JSON parsing, dedup, persistence)
  testable.
- `llm`: same orchestration with a real OpenAI-compatible model. Requires
  `OPENAI_API_KEY` (and optionally `OPENAI_BASE_URL`); pick the model with
  `--model` or the `MODEL_NAME` environment variable.

Model findings are validated before they are trusted: confidence is clamped,
severity/category are normalized, evidence is redacted, and findings that
reference files or lines outside the diff are downgraded to the human-review
bucket. A model failure never fails the review task; the run degrades to
rule-only results and records a `model_error` exception in the metrics.

## Run

From the `examples` module:

```bash
go run ./code_review_agent \
  --fixture security_secret \
  --mode rule-only \
  --out-dir /tmp/code-review-agent \
  --db /tmp/code-review-agent/review.db
```

Run the full agent chain without an API key:

```bash
go run ./code_review_agent \
  --fixture security_secret \
  --mode fake-model \
  --sandbox mock --dry-run \
  --out-dir /tmp/code-review-agent \
  --db /tmp/code-review-agent/review.db
```

Run with a real model:

```bash
export OPENAI_API_KEY=...
go run ./code_review_agent \
  --repo-path /path/to/repo \
  --mode llm \
  --model deepseek-v4-flash
```


Run all fixtures:

```bash
go run ./code_review_agent \
  --fixture all \
  --mode rule-only \
  --out-dir /tmp/code-review-agent-fixtures \
  --db /tmp/code-review-agent-fixtures/review.db
```

Review local working tree changes:

```bash
go run ./code_review_agent \
  --repo-path /path/to/repo
```

Repository input combines unstaged changes, staged changes, and untracked
regular files. Untracked symlinks, non-regular files, binary files, and files
outside the repository are skipped and recorded in the input summary. Explicit
`--files` input rejects those cases instead of silently skipping them.

Input is capped at 200 explicit/untracked files, 1 MiB per synthesized file,
and 10 MiB for the combined diff. Diff files and Git subprocess output are
bounded while they are read, so the limit also protects memory use.

Review an explicit file list:

```bash
go run ./code_review_agent \
  --repo-path /path/to/repo \
  --files internal/foo.go,internal/bar.go \
  --out-dir /tmp/code-review-agent-files \
  --db /tmp/code-review-agent-files/review.db
```

Query a persisted task:

```bash
go run ./code_review_agent \
  --db /tmp/code-review-agent/review.db \
  --task-id cr-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
```

The default sandbox is `managed`, which runs checks through
`codeexecutor/sandbox`. Use `--sandbox container` for Docker-backed
`codeexecutor/container`, and `--sandbox e2b` for `codeexecutor/e2b` (requires
E2B credentials such as `E2B_API_KEY`). Both the Go static checks and the
skill scripts (via the framework `skill_run` tool) run on the selected
sandbox. `mock` is available only for explicit
dry-run/testing paths. Use `--sandbox local-dev` only for local development; it
is intentionally not the default production-like path.

Every external command is gated by a governance policy that implements the
framework `tool.PermissionPolicy` interface; non-allow-listed commands are
denied or routed to human review, and every decision is persisted. Pass
`--staticcheck` to additionally run `staticcheck ./...` when the binary is
available in the sandbox.

## Outputs

- `review_report.json`
- `review_report.md`
- `artifact_manifest.json` with SHA-256 and byte size for the JSON and Markdown
  reports
- SQLite tables (behind the swappable `store.Store` interface):
  - `schema_migrations`
  - `review_tasks`
  - `review_findings`
  - `sandbox_runs`
  - `permission_decisions`
  - `filter_decisions`
  - `review_reports`
  - `artifacts`

Reports are written through temporary files, synced, and renamed into place.
The complete final audit snapshot is committed to SQLite in one transaction;
findings, sandbox runs, governance and filter decisions, artifact records, and
report metadata cannot be partially committed. The embedded SQLite driver is
pure Go and works when `CGO_ENABLED=0`. Schema version 1 enables foreign keys
and indexes task-owned audit rows.

## Fixtures

The fixtures cover clean diffs, secret leakage, goroutine/context leakage,
resource lifecycle issues, transaction lifecycle issues, missing tests,
duplicate findings, sandbox failure input, and redaction.

Regenerate the curated expected outputs after an intentional behavior change:

```bash
go run ./code_review_agent --fixture all --mode rule-only \
  --out-dir /tmp/code-review-agent-fixtures \
  --db /tmp/code-review-agent-fixtures/review.db
go run ./code_review_agent/testdata/gen_expected.go \
  /tmp/code-review-agent-fixtures ./code_review_agent/testdata/expected
```

## Design

See [DESIGN.md](DESIGN.md) (English) and
[DESIGN.zh_CN.md](DESIGN.zh_CN.md) (中文方案设计说明).
