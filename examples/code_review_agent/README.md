# Code Review Agent Example

This example is a self-contained prototype for Issue 2004. It demonstrates how
to combine a `code-review` Skill, diff parsing, sandbox execution governance,
SQLite-equivalent durable persistence, secret redaction, and JSON/Markdown
report generation into an auditable Go code-review agent.

The example is intentionally isolated under `examples/code_review_agent` and
does not expand the public API of the root framework.

## Run

```bash
cd examples/code_review_agent
go test ./...
OPENAI_API_KEY="..." MODEL="gpt-4.1-mini" \
  go run . -fixture-dir testdata/fixtures -out-dir ./out -model "$MODEL"
```

The CLI reads diff fixtures, records a review task, writes `review_report.json`
and `review_report.md`, and prints an English summary. Unit tests use mock
model and sandbox seams with `--runtime fake`; non-fake CLI runs validate
OpenAI-compatible model configuration through `OPENAI_API_KEY`,
`OPENAI_BASE_URL`, and `MODEL` or `--model`.

## Runtime Policy

- `container` and `e2b` are production-oriented runtime names.
- `local` is an explicit development fallback only.
- Tests use `fake` or dry-run execution to avoid Docker, E2B, and API-key
  dependencies.
- Non-fake runtimes require model orchestration configuration and fail fast in
  English when `MODEL` / `--model` or `OPENAI_API_KEY` is missing.
- Runtime initialization failure is recorded as a sandbox failure and should
  not silently fall back to `local`.

## Outputs

- `review_report.json`: structured findings, governance decisions, artifacts,
  and metrics.
- `review_report.md`: human-readable summary.
- `review_agent.db`: dependency-free durable task, input, sandbox run,
  permission decision, finding, artifact, and report records. The generated
  `.db` file is not checked in.
