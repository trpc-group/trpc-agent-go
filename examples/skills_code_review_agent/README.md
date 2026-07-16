# Skills Code Review Agent

This example is a deterministic Go code review agent prototype built from
existing tRPC-Agent-Go pieces:

- Agent Skills via `skill.NewFSRepository`
- workspace sandboxes via `codeexecutor/container`, `codeexecutor/e2b`, or
  opt-in local fallback
- `tool.PermissionPolicy` decisions before high-risk commands
- SQLite persistence for tasks, inputs, sandbox runs, permission decisions,
  findings, artifacts, reports, and audit metrics
- rule-only execution so the full path works without a model API key
- AST-assisted deterministic rules for Go lifecycle and error patterns
- hidden-sample-oriented rules for HTTP body leaks, SQL rows leaks,
  multi-line SQL concatenation, context misuse, and goroutine shared
  mutation
- sandbox diagnostic parsing back into structured findings
- schema migration tracking through `schema_migrations`
- telemetry spans around review orchestration and workspace runs
- artifact allowlist, count limit, and per-file size limit
- SQLite foreign keys, single-connection writes, and a `ReviewStore`
  interface so the example can swap SQL backends without changing the
  review pipeline
- optional `llmagent` review path with `skill.NewFSRepository` and a
  deterministic fake model for API-key-free Agent + Skill orchestration
- dry-run/fake execution still records PermissionPolicy decisions, which
  keeps the governance audit path testable without Docker or model keys

## Code Layout

The example stays in one Go module so it can be copied or run directly
from `examples/`, while implementation code lives under
`internal/review`:

- `main.go`: CLI flag parsing and call into the review package
- `cmd/review-agent`: product-style CLI with YAML config support
- `internal/review/reviewer.go`: orchestration, input loading, metrics,
  and persistence handoff
- `internal/review/parser.go`: unified diff, file-list, git worktree,
  hunk, and package extraction
- `internal/review/rules.go` and `internal/review/ast_rules.go`:
  deterministic review rules
- `internal/review/sandbox.go`, `sandbox_output.go`, and
  `permission.go`: workspace execution, command gating, sandbox
  diagnostics, and source-tree staging
- `internal/review/storage.go`: SQLite schema, migrations, and query API
  behind `ReviewStore`
- `internal/review/report.go`: JSON and Markdown report rendering
- `internal/review/llm_review.go`: supplemental Skill-backed model
  review with no code execution tool attached

## Run

From this directory:

```bash
go run . --fixture security_issue --dry-run
```

Reports are written to:

- `output/review_report.json`
- `output/review_report.md`
- `output/review_diagnostics.json`
- `output/review_report.zh.md`
- `output/reviews.sqlite`

Run through the product-style entrypoint and YAML config:

```bash
go run ./cmd/review-agent --config cr-agent.example.yaml
```

The config file demonstrates the same review kernel with named sections
for input, sandbox, output, and model providers. Supported provider
labels are `fake`, `openai`, `openai-compatible`, `http`, and
`deepseek`; the example defaults to rule-only/fake settings so it runs
without an API key.

Run against a working tree and execute real Go checks in Docker:

```bash
go run . --repo-path /path/to/repo --executor container
```

`--repo-path` reads unstaged, staged, and untracked file changes. A
newline-delimited path list can also be reviewed when only changed file
names are available:

```bash
go run . --file-list changed_files.txt --dry-run
```

`--dry-run` is intentionally a deterministic fake-sandbox mode. It
exercises diff parsing, rules, PermissionPolicy decisions, report
generation, SQLite writes, and failure bucketing without Docker, E2B, or
model keys. Use `--executor container`, `--executor e2b`, or
`--container-smoke` when you need to prove the real sandbox execution
path.

Run a deterministic container success smoke without any external module
downloads. This creates a temporary no-dependency Go repository, makes a
small production change plus a matching test change, then runs the real
container workflow:

```bash
go run . --container-smoke --output-dir output/container-smoke-latest
```

Expected sandbox result: `diff_summary.sh`, `go test ./...`, and
`go vet ./...` succeed. The smoke path preinstalls staticcheck and the
integration proof asserts that `staticcheck ./...` succeeds instead of
being recorded as an unavailable optional tool.

If Docker Hub is slow or unavailable, use a regional mirror for the
sandbox base image:

```bash
go run . --repo-path /path/to/repo --executor container \
  --container-base-image docker.m.daocloud.io/library/golang:1.23-bookworm
```

For arbitrary `--repo-path` reviews, staticcheck remains optional. To
make `staticcheck ./...` succeed instead of being recorded as an
unavailable tool, pass a base image that already contains `staticcheck`
on `PATH`, or build it into the sandbox image. The install flag is best
used when prebuilding the sandbox image for CI, because compiling
staticcheck can take noticeably longer than a normal review run:

```bash
go run . --repo-path /path/to/repo --executor container \
  --container-base-image docker.m.daocloud.io/library/golang:1.23-bookworm \
  --container-install-staticcheck
```

Use E2B:

```bash
go run . --repo-path /path/to/repo --executor e2b
```

The E2B path is wired through `codeexecutor/e2b`, but the local
validation for this example only exercised dry-run and container smoke.
Run the command above with a valid `E2B_API_KEY` in CI or a cloud
environment before claiming E2B production verification.

Use local execution only as development fallback:

```bash
go run . --repo-path /path/to/repo --executor local --allow-local-fallback
```

Run the Agent + Skill path with a deterministic fake model:

```bash
go run . --fixture no_issue --dry-run --fake-model
```

Run a real model-backed supplemental review:

```bash
export OPENAI_API_KEY=sk-...
go run . --fixture security_issue --rule-only=false --model gpt-4o-mini --dry-run
```

Run an OpenAI-compatible or DeepSeek endpoint:

```bash
export OPENAI_API_KEY=sk-...
go run ./cmd/review-agent --fixture security_issue --dry-run \
  --rule-only=false --model-provider openai-compatible \
  --model-base-url https://api.example.com/v1 --model custom-review-model

export DEEPSEEK_API_KEY=sk-...
go run ./cmd/review-agent --fixture security_issue --dry-run \
  --rule-only=false --model-provider deepseek --model deepseek-chat
```

The deterministic rule engine still runs first. Model findings are
deduplicated with the same file/line/category key and low-confidence
items are placed in `warnings` or `needs_human_review`, not mixed into
high-confidence findings.

## Fixtures

All fixtures can run without Docker:

```bash
./scripts/run_all_fixtures.sh
```

The script writes one report directory per fixture under
`output/fixtures/` and a `summary.tsv` containing finding counts,
sandbox run counts, permission decision counts, and explicit
permission `needs_human_review` counts.

For a one-command proof that runs unit tests, vet, the fixture matrix,
and a real container smoke when Docker is available:

```bash
./scripts/integration_proof.sh
```

The current proof is summarized in `INTEGRATION_PROOF.md`. The holdout
quality regression lives under `internal/review/testdata/holdout` and is
checked by `TestHoldoutFixtureQualityThresholds`.

To exercise sandbox failure handling deterministically:

```bash
go run . --fixture sandbox_failure --executor fake-fail
```

## Database

SQLite tables:

- `review_tasks`
- `schema_migrations`
- `review_inputs`
- `sandbox_runs`
- `permission_decisions`
- `findings`
- `artifacts`
- `reports`
- `audit_metrics`

All child tables reference `review_tasks(id)` with `ON DELETE CASCADE`
in fresh databases. The SQLite handle enables `PRAGMA foreign_keys=ON`
and uses a single open connection, matching the write pattern used by
the review pipeline.

Query a task:

```bash
sqlite3 output/reviews.sqlite \
  'select id, status, input_mode from review_tasks order by started_at desc limit 5;'
```

## Safety Model

Container is the default executor. Local execution is denied unless
`--allow-local-fallback` is set. Every sandbox command, including the
audited `skills/code-review/scripts/diff_summary.sh` helper, is checked
by a PermissionPolicy before execution. The runner stages the
`code-review` Skill into the workspace, uses per-command timeouts, clean
environment variables, output truncation, secret redaction, and failure
records so sandbox errors do not crash the review task. Dry-run/fake
mode records the same permission decision shape while marking execution
as skipped; it is a CI-friendly governance and persistence check, not a
replacement for container/E2B validation. Staticcheck is optional; when
it is unavailable the run is recorded instead of making the image build
depend on network access. For `--repo-path`, the runner stages a
sanitized temporary snapshot instead of the raw host directory: git
metadata, ignored files, symlinks, local env files, private keys, and
secret-like paths are excluded before the tree enters the sandbox. The
supplemental `llmagent` path is configured with knowledge-only Skill
tools and no CodeExecutor; all executable checks stay behind the sandbox
runner and PermissionPolicy.

## Compared With Other #2004 PRs

The local implementation keeps the strongest parts of the competing
approaches while staying self-contained in this example directory:

- it uses the real `skill` repository loader and `codeexecutor`
  container/E2B engines instead of a mock-only sandbox;
- it records complete SQLite state for tasks, inputs, sandbox runs,
  permission decisions, findings, artifacts, reports, and metrics;
- it includes a real `llmagent` + Skill loading path plus a fake model
  smoke mode, so Agent orchestration is testable without API keys;
- it borrows the fail-closed permission posture and hidden-sample rule
  breadth seen in other submissions without adding new framework-wide
  packages;
- it runs fully in deterministic rule-only/fake-sandbox mode for CI and
  still supports production-style container execution by default.
