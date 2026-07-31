# Code Review Agent Example

This nested module is a deterministic Go code-review agent example. It parses a
unified diff, applies bundled rule and script checks, gates execution with an
allow/deny/ask permission decision before staging, writes a SQLite audit trail,
and renders redacted JSON and Markdown reports.

Run the no-key fake path:

```bash
cd examples/code_review_agent
go run . --runtime=fake --mode=rule-only --fixture=security --out=out --db=out/review_audit.db
```

Run a diff file:

```bash
go run . --runtime=fake --diff-file=testdata/fixtures/secret_redaction.diff --out=out
```

Exercise the public Agent and Skill tool-call path with the deterministic
scripted model:

```bash
go run . --runtime=fake --mode=agent --fixture=security --out=out
```

Run the default container path when Docker is available. The container runtime
builds `examples/code_review_agent/Dockerfile`, which provides Go and
`staticcheck` for the bundled checks:

```bash
go run . --runtime=container --repo-path=/path/to/go/repository --out=out
```

Limit a repository review to literal paths, either repeatedly or from a
newline-delimited list:

```bash
go run . --runtime=fake --repo-path=. --file=internal/a.go --file=cmd/app/main.go
go run . --runtime=fake --repo-path=. --file-list=changed-files.txt
```

Show an audited task:

```bash
go run . --db=out/review_audit.db --show-task=<task_id>
```

The `local` runtime is a development-only trust boundary and refuses to start
unless `--allow-local` is passed. The production default is `container`; it does
not silently fall back to local execution when Docker is unavailable.

The execution plan binds 60-second command and 120-second task deadlines,
per-stream 1 MiB output limits, and artifact count/size limits. Bundled check
scripts capture stdout and stderr into temporary files and only replay bounded
content plus `output_truncated:*` markers back to the runtime; the report also
records `truncated` and `truncation_reason`. Container memory, CPU, PID,
network, capability, and `no-new-privileges` controls are set in Docker
`HostConfig`.

Outputs:

- `acceptance_manifest.json`
- `review_report.json`
- `review_report.md`
- SQLite tables: `review_tasks`, `sandbox_runs`, `governance_decisions`,
  `findings`, `artifacts`, `review_metrics`, `reports`

`acceptance_manifest.json` is the machine-readable acceptance entry point. It
contains the task id, status, input summary, stats, metrics, artifact metadata,
and sandbox/redaction/governance check states. Artifact rows include sha256,
byte size, content type, and durability.

Hidden-sample precision/recall targets cannot be proven by this repository
alone; those require the event evaluator's hidden corpus. The local tests cover
public fixtures and deterministic holdout-style checks so reviewers can verify
the pipeline without private data.

File-level findings use `line: 0`, currently for missing related tests. The
example does not fetch GitHub PRs or post comments; `--diff-file` accepts a
normal unified diff or PR patch.
