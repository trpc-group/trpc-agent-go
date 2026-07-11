# Code Review Agent

This example is an automated code review agent built on trpc-agent-go's Skills, sandboxed execution and storage capabilities. It ingests a diff or an explicit file list, runs a `code-review` skill against each target inside a fail-closed sandbox (container / e2b / local), applies a permission-controlled rule set, deduplicates findings, persists them to SQLite and emits a Markdown/JSON report.

## Usage

Build the example:

```sh
go build ./...
```

Run the tests:

```sh
go test ./... -count=1 -race
```

Verify a CGO-free build (the example uses the pure-Go `modernc.org/sqlite` driver so no C toolchain is required):

```sh
CGO_ENABLED=0 go build ./...
```

Dry-run the skeleton against a clean fixture (parses inputs and plans the review without executing sandboxed tools):

```sh
go run . --dry-run --diff-file ./testdata/fixtures/clean.diff --out-dir ./out --db-path ./review.db
```

## Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--diff-file` | string | `""` | Path to a unified diff file to review. |
| `--repo-path` | string | `""` | Path to the repository under review. |
| `--file-list` | string | `""` | Path to a newline-separated list of files to review. |
| `--fixture-dir` | string | `""` | Path to a fixture directory used for dry-run inputs. |
| `--out-dir` | string | `./out` | Directory where review artifacts are written. |
| `--db-path` | string | `./review.db` | Path to the SQLite database used for persistence. |
| `--executor` | string | `container` | Sandbox executor backend: `container`, `e2b` or `local`. |
| `--unsafe-local` | bool | `false` | Allow the unsafe local executor (fail-closed by default). |
| `--dry-run` | bool | `false` | Parse inputs and plan the review without executing sandboxed tools. |
| `--model` | string | `deepseek-v4-flash` | LLM model identifier used by the review skill. |

## Architecture

The example is organised into the following internal packages:

| Package | Responsibility |
| --- | --- |
| `diffparse` | Parses unified diffs into hunks with file paths and line ranges. |
| `inputsource` | Resolves review targets from `--diff-file`, `--repo-path` and `--file-list`. |
| `rules` | Defines the review rule set and the matching engine. |
| `review` | Orchestrates the `code-review` skill flow, LLM calls and tool dispatch. |
| `redact` | Scrubs secrets and sensitive tokens from inputs and findings. |
| `permission` | Token-level allow-list matching for sandboxed tool calls (fail-closed). |
| `sandbox` | Wraps `codeexecutor.Engine` for container/e2b/local execution and cleanup. |
| `store` | SQLite persistence layer (schema, migrations, finding upsert). |
| `telemetry` | Collects timing, tool-call, interception and severity metrics. |
| `report` | Renders Markdown/JSON review reports from persisted findings. |
