# Code Review Agent Example

A Go code-review agent built with `trpc-agent-go`.

Given a change (unified diff, local Git worktree, or packaged fixture),
the agent loads the `code-review` Skill, may run checks in a container
sandbox after permission approval, records the task in SQLite, and
writes `review_report.json` and `review_report.md`.

Real-model and fake-model runs use the same pipeline. Fake model only
replaces the model so you can exercise the rest without an API key:

```text
Runner → LLMAgent → Skill → PermissionPolicy
  → request_tool_permission → workspace_exec
  → SQLite / Artifact → report
```

## Prerequisites

- Go 1.25+ (or a compatible toolchain)
- Docker (for `--sandbox container`)
- An OpenAI-compatible API key for real-model runs (defaults below use
  DeepSeek)

Container mode builds `docker/Dockerfile` into the local image tag
`code-review-agent-sandbox:latest` when the container executor starts.
Docker layer cache usually makes later builds cheap. The Dockerfile sets
the offline Go environment; Docker pulls the base image if needed.

## Flags

| Flag           | Description                                    | Default             |
| -------------- | ---------------------------------------------- | ------------------- |
| `--mode`       | `fake-model` for deterministic offline runs    | ``                  |
| `--model`      | Model name                                     | `deepseek-v4-flash` |
| `--api-key`    | Model API key (real-model mode)                | ``                  |
| `--base-url`   | Model API base URL (real-model mode)           | ``                  |
| `--sandbox`    | `container` or `local`                         | `container`         |
| `--db-path`    | SQLite database path                           | `cr.db`             |
| `--diff-file`  | Unified diff or PR patch                       | ``                  |
| `--repo-path`  | Local Git worktree (input and/or repo context) | ``                  |
| `--fixture`    | Named fixture under `testdata/fixtures`        | ``                  |
| `--paths`      | Comma-separated repo-relative path scope       | ``                  |
| `--paths-file` | File with one repo-relative path per line      | ``                  |
| `--output-dir` | Directory for per-task reports                 | `review-output`     |

Input rules:

- Use `--fixture`, or use `--diff-file` and/or `--repo-path`.
- Do not combine `--fixture` with `--diff-file` or `--repo-path`.
- `--paths` / `--paths-file` optionally limit scope for repo or diff input.
- Prefer `--sandbox container`. `--sandbox local` is a host fallback for
  development only.

On success the process prints the task id and absolute paths:

- `<output-dir>/<task-id>/review_report.json`
- `<output-dir>/<task-id>/review_report.md`

The same pair is also stored via Artifact Service.

## Quick start (fake model, no API key)

```bash
cd examples/code_review_agent

go run . \
  --mode fake-model \
  --fixture acceptance-security \
  --sandbox container \
  --db-path cr.db \
  --output-dir review-output
```

This still loads the Skill, stages a task workspace, runs real
`workspace_exec`, applies PermissionPolicy, writes SQLite and
artifacts, and emits both reports. Fake-model mode auto-approves
permission prompts through the same pipeline.

Fixtures:

| Fixture                         | Focus                               |
| ------------------------------- | ----------------------------------- |
| `acceptance-clean`              | No issues                           |
| `acceptance-security`           | Command injection                   |
| `acceptance-context-leak`       | Missing context cancellation        |
| `acceptance-database-lifecycle` | DB / connection lifecycle           |
| `acceptance-duplicate-finding`  | Result conflict + retry merge       |
| `acceptance-missing-tests`      | Missing tests for new behavior      |
| `acceptance-resource-leak`      | Resource lifecycle                  |
| `acceptance-sandbox-failure`    | Failed checks still complete review |
| `acceptance-secret-redaction`   | Secret masking                      |

## Real model

```bash
cd examples/code_review_agent

export MODEL_API_KEY='<your-api-key>'

go run . \
  --model deepseek-v4-flash \
  --base-url https://api.deepseek.com \
  --api-key "$MODEL_API_KEY" \
  --sandbox container \
  --fixture acceptance-security \
  --db-path cr.db \
  --output-dir review-output
```

When the agent needs the Skill baseline script
(`skills/code-review/scripts/run-go-checks.sh`), the terminal prompts:

```text
The review agent requests permission to use a governed tool.
Target tool: workspace_exec
Target arguments: ...
Reason: ...
Approve? [Y/n]
```

Press Enter or `y` to approve, `n` to deny. Without a grant the command
does not run; the agent must continue with other evidence and must not
claim the check succeeded.

## Input modes

### Patch only

```bash
go run . \
  --diff-file ./change.patch \
  --model deepseek-v4-flash \
  --base-url https://api.deepseek.com \
  --api-key "$MODEL_API_KEY" \
  --sandbox container
```

### Local Git worktree

Reviews staged, unstaged, and untracked (non-ignored) changes relative
to `HEAD`:

```bash
go run . \
  --repo-path /absolute/path/to/git-worktree \
  --model deepseek-v4-flash \
  --base-url https://api.deepseek.com \
  --api-key "$MODEL_API_KEY" \
  --sandbox container \
  --output-dir review-output
```

Optional path scope:

```bash
go run . \
  --repo-path /absolute/path/to/git-worktree \
  --paths internal/reviewer,main.go \
  --model deepseek-v4-flash \
  --base-url https://api.deepseek.com \
  --api-key "$MODEL_API_KEY" \
  --sandbox container
```

### Diff plus repository context

```bash
go run . \
  --diff-file ./pr.patch \
  --repo-path /absolute/path/to/git-worktree \
  --model deepseek-v4-flash \
  --base-url https://api.deepseek.com \
  --api-key "$MODEL_API_KEY" \
  --sandbox container
```

## Review this example with itself

This dogfooding run presents every Git-managed file under
`examples/code_review_agent` as a newly added project while retaining a
clean monorepo checkout as repository context. That keeps sibling
`replace` targets available and deliberately excludes untracked local
files. Add any intended new source files to the Git index before
running this procedure.

```bash
cd examples/code_review_agent

EXAMPLE="$PWD"
REPO="$(git rev-parse --show-toplevel)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/code-review-agent-self.XXXXXX")"
SUBJECT="$WORK/subject"
mkdir -p "$WORK/out"

# 1) Create a clean local monorepo copy with no untracked source files.
git clone --quiet --local --no-hardlinks "$REPO" "$SUBJECT"

# 2) Commit a baseline in which this example does not exist.
git -C "$SUBJECT" rm -r --quiet --ignore-unmatch \
  examples/code_review_agent
git -C "$SUBJECT" \
  -c user.email=self@local \
  -c user.name=self \
  commit --quiet -m "baseline without code_review_agent"

# 3) Copy the current contents of Git-managed example files.
git -C "$REPO" ls-files -z --cached -- \
  examples/code_review_agent |
  tar -C "$REPO" --null -T - -cf - |
  tar -C "$SUBJECT" -xf -
git -C "$SUBJECT" add -A -- examples/code_review_agent

# Optional: inspect the exact full-example change that will be reviewed.
git -C "$SUBJECT" diff --cached --stat -- \
  examples/code_review_agent

# 4) Run the agent from the original example directory.
cd "$EXAMPLE"
export MODEL_API_KEY='<your-api-key>'

go run . \
  --repo-path "$SUBJECT" \
  --paths examples/code_review_agent \
  --model deepseek-v4-flash \
  --base-url https://api.deepseek.com \
  --api-key "$MODEL_API_KEY" \
  --sandbox container \
  --db-path "$WORK/out/cr.db" \
  --output-dir "$WORK/out/review-output"
```

Approve governed Skill baseline requests with Enter or `y`. On success,
the process prints:

```text
Review completed
Task ID: review-<task-id>
JSON report: <output-dir>/<task-id>/review_report.json
Markdown report: <output-dir>/<task-id>/review_report.md
```

The clean monorepo copy preserves local `replace` targets. The container
still runs with `GOPROXY=off`, so checks that need uncached external
modules may fail. Such a failure is recorded as sandbox evidence and
does not by itself become a code finding.

### Code Review Report for example itself

```markdown
# Code Review Report

Task: `review-<task-id>`

Status: completed

Input: repo_path

## Conclusion

No code defects found in this new example project. The code is
well-structured with proper error handling, resource cleanup (defer
close patterns), SQL parameterization, transaction lifecycle management
(defer rollback + explicit commit), database row lifecycle (defer
rows.Close + rows.Err()), concurrency safety (mutex, sync.Once),
comprehensive secret redaction, and a well-designed
governance/permission system. The baseline go test/go vet check failed
due to GOPROXY=off in the sandbox environment preventing dependency
downloads, which is an environment limitation, not a code defect. The
example is designed to work in a container sandbox with pre-cached Go
modules.

## Summary

- Findings: 0
- Warnings: 0
- Needs human review: 0
- Tool calls: 50
- Permission interceptions: 5
- Total duration: 305994 ms
- Sandbox duration: 61791 ms
- Severity distribution: none
- Exception distribution: nonzero_exit=3

## Findings

None.

## Warnings

None.

## Needs Human Review

None.

## Governance Interceptions

- ask `workspace_exec` — `cat work/inputs/repo/examples/code_review_agent/skills/code-review/scripts/run-go-checks.sh`: Call `request_tool_permission` with this target tool and its exact arguments, then retry the target tool only if permission is granted.
- ask `workspace_exec` — `skills/code-review/scripts/run-go-checks.sh work/inputs/repo/examples/code_review_agent`: Call `request_tool_permission` with this target tool and its exact arguments, then retry the target tool only if permission is granted.
- ask `workspace_exec` — `skills/code-review/scripts/run-go-checks.sh /workspace/work/inputs/repo/examples/code_review_agent`: Call `request_tool_permission` with this target tool and its exact arguments, then retry the target tool only if permission is granted.

## Sandbox Runs

- `find work/inputs/repo/examples/code_review_agent -type f -name "*.go" -o -name "go.mod" | sort` — succeeded, exit=0, duration=1596 ms, truncated=false
- `cat work/inputs/repo/examples/code_review_agent/main.go` — succeeded, exit=0, duration=1660 ms, truncated=false
- `cat work/inputs/repo/examples/code_review_agent/internal/reviewer/reviewer.go` — succeeded, exit=0, duration=1860 ms, truncated=false
- `cat work/inputs/repo/examples/code_review_agent/internal/reviewer/governance.go` — succeeded, exit=0, duration=1477 ms, truncated=false
- `cat work/inputs/repo/examples/code_review_agent/internal/persistence/persistence.go` — succeeded, exit=0, duration=1700 ms, truncated=false
- `cat work/inputs/repo/examples/code_review_agent/internal/store/store.go` — succeeded, exit=0, duration=1638 ms, truncated=false
- `cat work/inputs/repo/examples/code_review_agent/skills/code-review/scripts/run-go-checks.sh` — succeeded, exit=0, duration=1657 ms, truncated=false
- `skills/code-review/scripts/run-go-checks.sh work/inputs/repo/examples/code_review_agent` — failed, exit=1, duration=5229 ms, truncated=true
```

## Persistence and inspection

One CLI run creates one review task. The task id is also the Session
Service `session_id`. SQLite holds the task record, permission
decisions, sandbox runs, and result snapshot; Session Service holds the
event trail; Artifact Service holds report bodies.

Schema: [internal/persistence/DDL.sql](internal/persistence/DDL.sql).

Latest task:

```bash
sqlite3 -header -column cr.db '
SELECT task_id, task_status, input_kind, conclusion,
       json_report_name, json_report_version,
       markdown_report_name, markdown_report_version
FROM review_tasks
ORDER BY created_at DESC
LIMIT 1;'
```

Per-task detail (set `TASK_ID`):

```bash
sqlite3 -header -column cr.db \
  "SELECT * FROM permission_decisions WHERE task_id = '$TASK_ID' ORDER BY id;"

sqlite3 -header -column cr.db \
  "SELECT * FROM sandbox_runs WHERE task_id = '$TASK_ID' ORDER BY id;"

sqlite3 -header -column cr.db \
  "SELECT * FROM review_results WHERE task_id = '$TASK_ID' ORDER BY id;"

sqlite3 -json cr.db \
  "SELECT monitoring_summary_json FROM review_tasks WHERE task_id = '$TASK_ID';"
```

## Tests

```bash
cd examples/code_review_agent

go test ./...
go test -race ./...
go vet ./...
```

## Layout

```text
examples/code_review_agent/
├── main.go                 
├── docker/Dockerfile       # Dockerfile
├── internal/
│   ├── reviewer/           # Orchestration, governance, reports
│   ├── reviewinput/        # CR input preparation
│   ├── fakemodel/          # Fake model
│   ├── store/              # Review Store (SQLite)
│   ├── persistence/        # Session + store + artifact wiring, DDL
│   ├── artifact/           # SQLite artifact adapter
│   └── redact/             # Secret redaction
├── skills/code-review/     
└── testdata/
    ├── fixtures/           
    └── example-output/     
```
