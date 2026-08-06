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
repository-relative paths. With a usable Git `HEAD`, it filters the comparison:
selected tracked files use their real diff against `HEAD`, while selected
untracked files are represented as whole-file additions. In a non-Git or
no-HEAD directory, all selected files are whole-file additions. Repository
snapshots contain tracked files plus non-ignored untracked files; ignored files
and `.git` metadata are excluded. An explicit file list may opt in an otherwise
ignored file, which is also represented as a whole-file addition.

Each run writes `review_report.json`, `review_report.md`, and SQLite state.
Use a fresh, caller-private `--output-dir` for each run; report files are
atomically published and never overwritten. Do not share that directory with
unrelated writers.
`GetReview(taskID)` returns task, input summary, sandbox runs, governance
decisions, findings, metrics, durable artifacts, and canonical reports from one
read-only SQLite transaction, so a concurrent finalization cannot produce a
mixed aggregate. Raw diff and source are never persisted. Findings are
normalized by bucket and deduplicated by cleaned file, line, and category.
Sandbox result files are private workspace temporaries and are never reported
as durable artifacts; their validated SHA-256 and size are stored on each run.

## Security model

Every sandbox check follows `workspace creation -> safety filter ->
PermissionPolicy -> durable decisions -> read-only copy staging -> execution`.
Filter and Permission inspect the exact workspace paths and runtime values.
The reviewed repository is not bind-mounted into the container and is copied
only after the durable allow decision. Container execution uses no network, a
read-only work tree, bounded tmpfs/resources/output, a trusted runner, exact
artifact collection, and unconditional cleanup. Deny, ask, policy error, or
decision persistence failure prevents staging and execution. Secret redaction
is applied again at report, SQLite, CLI, telemetry, error, and artifact
boundaries.
The host snapshot root remains caller-private. Staged files are readable by the
sandbox UID, preserve executable semantics, and remain non-writable.

For Go dependencies, the host cache is never mounted wholesale. Every changed
old and new path selects a bounded, sorted set of module roots; deterministic
source analysis still accepts only Go files. Those roots are included in the
governance decision, and each
receives the same fixed check under one total timeout and output limit.
`GOWORK=off` prevents an ambient workspace from widening that set. The
application selects only checksummed versions from every selected module's
`go.sum`, verifies official module zip and `go.mod` hashes plus aggregate size
limits, and mounts a per-check `file://` module proxy read-only. The target
process gets a fresh writable module cache in tmpfs while container networking
remains disabled. Missing or invalid cached dependencies become a recorded
`dependency_cache` sandbox failure.

Command execution is bounded by the caller context, the trusted runner timeout,
and `RunProgramSpec.Timeout`. The shared container runtime provides
`NewWithContext` and `CloseWithContext`; legacy `New` and `Close` preserve
background-context behavior. This example uses context-aware construction and a
separate bounded cleanup context so caller cancellation does not abandon
container teardown.

`total_duration_ms` equals `task.finished_at - task.started_at` and ends when the
validated review snapshot becomes immutable, immediately before external report
publication and SQLite finalization. The same finished time and metric snapshot
are used by the final report, database record, and telemetry.

SQLite databases created before sandbox result evidence was introduced are
migrated transactionally. Legacy `check-result` artifact digests and sizes move
onto their sandbox runs, the now-dead temporary artifact rows are removed, and
reports that embedded those rows are regenerated without temporary paths.
External report paths are cleared because those older copies are stale.

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
