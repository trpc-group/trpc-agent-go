# Code Review Agent Example

This example is a self-contained prototype for Issue 2004. It demonstrates how
to combine a `code-review` Skill, diff parsing, sandbox execution governance,
JSON-backed durable persistence, secret redaction, and JSON/Markdown
report generation into an auditable Go code-review agent.

The example is intentionally isolated under `examples/code_review_agent` and
does not expand the public API of the root framework.

## Run

```bash
cd examples/code_review_agent
go test ./...
export OPENAI_API_KEY="..."
export MODEL="gpt-4.1-mini"
go run . -fixture-dir testdata/fixtures -out-dir ./out -model "$MODEL" -runtime fake
```

Supported input modes:

- `--fixture-dir testdata/fixtures` for deterministic public samples.
- `--diff-file path/to/change.diff` for a unified diff; add
  `--repo-path path/to/repo` when sandbox checks should run against the patched checkout.
- `--repo-path path/to/repo` for `git diff --no-ext-diff --binary`; sandbox commands run in that repository.
- `--file-list path/to/files.txt --repo-path path/to/repo` for a newline-delimited changed-file list tied to the repository that owns those paths. Empty lines and lines beginning with `# ` or `#\t` are comments; prefix a literal path beginning with either comment form with `\` (for example, `\# config.go`). Other non-empty lines are literal paths. Without `--repo-path`, sandbox validation is skipped. The repository path controls planner context, sandbox CWD, and `go test`/`go vet` scope; content-based deterministic rules require diff input.

The CLI reads diff fixtures, records a review task, writes task-specific
`review_report_<task-id>.json` and `review_report_<task-id>.md` artifacts, and
prints an English summary. The default `fake` runtime uses mock planning and
sandbox seams for deterministic local validation. Enabled non-fake runtimes
require `OPENAI_API_KEY`, optional `OPENAI_BASE_URL`, and `MODEL` or `--model`.

## Runtime Policy

- `fake` is the default runnable runtime and uses deterministic mock planning and sandbox execution.
- `container` is currently fail-closed in this example because the upstream `codeexecutor/container.New` API cannot receive cancellation during image pull/create/start or guarantee cleanup of partial initialization. It must remain unavailable until an upstream context-aware constructor with partial-initialization cleanup is provided; no fallback to host execution is performed.
- `e2b` is disabled by default because this example cannot enforce an egress boundary there; use `--allow-trusted-remote` only for explicitly trusted input when a networked remote runtime is acceptable.
- `local` is disabled for untrusted review input. Use `--allow-trusted-local` only when the reviewed repository is explicitly trusted; this opt-in permits host execution through `WorkspaceModeTrustedLocal`.
- Host-side dependency preparation is disabled by default unless dependencies are vendored or pre-provisioned in the review snapshot. Use `--allow-trusted-host-preparation` only for explicitly trusted input when `go mod download` on the host is acceptable.
- Non-fake runtimes call an OpenAI-compatible chat completions endpoint to plan
  Skill rules and sandbox commands, and fail fast in English when model
  configuration is missing or the planner call fails.
- Runtime policy and initialization failures are recorded on the failed task;
  the example never silently falls back to `local`.

## Outputs

- `review_report_<task-id>.json`: structured findings, governance decisions, artifacts,
  and metrics.
- `review_report_<task-id>.md`: human-readable summary.
- `review_agent.db`: the current dependency-free JSON-backed durable store for
  task, input, sandbox run, permission decision, finding, artifact, and report
  records. Despite the `.db` suffix, this file is not an SQLite database and is
  not checked in.
- `internal/store/schema.sql`: SQLite-compatible target schema for a future
  strict SQL storage backend; it is not the active persistence format.
