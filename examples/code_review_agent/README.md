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
- `OPENAI_API_KEY` set to an OpenAI-compatible API key for real-model
  runs (defaults below use DeepSeek)

Container mode builds `docker/Dockerfile` into the local image tag
`code-review-agent-sandbox:latest` when the container executor starts.
Docker layer cache usually makes later builds cheap. The Dockerfile sets
the offline Go environment; Docker pulls the base image if needed.

## Flags

| Flag           | Description                                    | Default             |
| -------------- | ---------------------------------------------- | ------------------- |
| `--mode`       | `fake-model` for deterministic offline runs    | ``                  |
| `--model`      | Model name                                     | `deepseek-v4-flash` |
| `--base-url`   | Model API base URL (real-model mode)           | ``                  |
| `--sandbox`    | `container` or `local`                         | `container`         |
| `--db-path`    | SQLite database path                           | `cr.db`             |
| `--diff-file`  | Unified diff or PR patch                       | ``                  |
| `--repo-path`  | Local Git worktree (input and/or repo context) | ``                  |
| `--fixture`    | Named fixture under `testdata/fixtures`        | ``                  |
| `--paths`      | Comma-separated repo-relative path scope       | ``                  |
| `--paths-file` | File with one repo-relative path per line      | ``                  |
| `--output-dir` | Directory for per-task reports                 | `review-output`     |

Real-model runs read the API key only from `OPENAI_API_KEY`. The CLI
does not accept API keys as flags, so credentials are not exposed in
process arguments.

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

printf 'OPENAI_API_KEY: '
read -s OPENAI_API_KEY
printf '\n'
export OPENAI_API_KEY

go run . \
  --model deepseek-v4-flash \
  --base-url https://api.deepseek.com \
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
  --sandbox container
```

### Diff plus repository context

```bash
go run . \
  --diff-file ./pr.patch \
  --repo-path /absolute/path/to/git-worktree \
  --model deepseek-v4-flash \
  --base-url https://api.deepseek.com \
  --sandbox container
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
