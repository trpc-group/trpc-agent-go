# Automatic Go Code Review Agent

This example combines tRPC-Agent Skills, deterministic Go review rules,
governed sandbox checks, SQLite persistence, audit metrics, and versioned
JSON/Markdown reports. It requires no model API key.

## Quick start

Run from this directory. Production execution defaults to the hardened
container runtime and requires Docker Desktop plus the local image build:

```powershell
go run . --fixture composite --runtime=container
```

Deterministic development modes execute the same Skill, governance,
persistence, and report chain:

```powershell
go run . --fixture composite --runtime=fake --dry-run
go run . --fixture composite --runtime=fake --fake-model
go run . --diff-file fixtures/composite/input.diff --runtime=fake
go run . --repo-path C:\src\project --runtime=container
go run . --repo-path C:\src\project --files-file changed.txt --runtime=container
```

`--runtime=local` is an unsafe development fallback only. It requires
`--allow-local`, records high-risk network and host-write capabilities, and uses
fixed commands, bounded output, timeout, and a clean environment. It runs on the
host repository and cannot guarantee container isolation or descendant-process
cleanup. It is never the default or a production sandbox.

The nine required public samples live in `fixtures/diffs/*.diff`; they share the
table-driven evaluator instead of duplicating repository trees. The
`composite` fixture is the single end-to-end repository sample.

## Inputs and outputs

Exactly one of `--diff-file`, `--repo-path`, or `--fixture composite` is
required. Repository mode supports staged, unstaged, untracked, rename,
delete, binary, and no-HEAD repositories. `--files-file` accepts only validated
repository-relative paths.

Each run writes `review_report.json`, `review_report.md`, and SQLite state.
Use a fresh `--output-dir` for each run; report files are never overwritten.
`GetReview(taskID)` returns task, input summary, sandbox runs, governance
decisions, findings, metrics, artifacts, and canonical reports. Raw diff and
source are never persisted. Findings are normalized by bucket and deduplicated
by cleaned file, line, and category.

## Security model

Every sandbox check follows `workspace creation -> safety filter ->
PermissionPolicy -> durable decisions -> read-only staging -> execution`.
Filter and Permission inspect the exact workspace paths and runtime values.
Container execution uses no network, a read-only staging source and work tree,
bounded tmpfs/resources/output, a trusted runner,
exact artifact collection, and unconditional cleanup. Deny, ask, policy error,
or decision persistence failure prevents execution. Secret redaction is applied
again at report, SQLite, CLI, telemetry, error, and artifact boundaries.

Command execution is bounded by the caller context, the trusted runner timeout,
and `RunProgramSpec.Timeout`. The shared container runtime's existing `New` and
`Close` APIs do not accept a caller context, so image initialization and final
container close inherit that shared lifecycle limitation; this example does not
wrap those calls in goroutines or change `codeexecutor/container`.

`total_duration_ms` equals `task.finished_at - task.started_at` and ends when the
validated review snapshot becomes immutable, immediately before external report
publication and SQLite finalization. The same finished time and metric snapshot
are used by the final report, database record, and telemetry.

## Verification

Use the standard Go commands; no project-specific verification wrapper is
required:

```powershell
gofmt -l .
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

The table-driven tests enforce fixture quality, report parity, SQLite
queries, governance ordering, output bounds, and secret redaction.
Real Docker acceptance is enabled separately with
`CODE_REVIEW_DOCKER_TEST=1 go test ./internal/sandbox -run RealDocker` and
`CODE_REVIEW_DOCKER_TEST=1 go test . -run TestRunCompositeRealDocker`.
In PowerShell, set `$env:CODE_REVIEW_DOCKER_TEST='1'` before both commands.

See [DESIGN.md](DESIGN.md), [Skill instructions](skills/code-review/SKILL.md),
and [rule documentation](skills/code-review/docs/rules.md).
