# Skills Code Review Agent

This example combines an Agent Skill, governed workspace execution,
deterministic Go review rules, SQLite persistence, and JSON/Markdown reports.
It does not require a model API key.

## Features

- Unified diff, PR patch, selected file, and Git worktree input
- Go-specific security, concurrency, context, resource, database, error, and
  test coverage rules
- Production-default `codeexecutor/container` runtime with no network access
- `tool.PermissionPolicy` decisions persisted before every command
- Time, process, memory, output, environment, snapshot, and artifact limits
- Secret redaction before report or database persistence
- Finding deduplication and a separate human-review queue
- Queryable tasks, inputs, sandbox runs, decisions, findings, artifacts,
  reports, and metrics
- Deterministic `--dry-run`, `--fake-model`, and `--runtime fake` modes

## Quick Start

The SQLite driver requires CGO at runtime. Docker is required for the default
production path.

```bash
cd examples/skills_code_review_agent

# No Docker or API key. Completes the full orchestration and persistence path.
go run . \
  --fixture security \
  --dry-run \
  --output-dir /tmp/code-review-output

# Review a real worktree in the isolated container.
go run . \
  --repo-path /path/to/go/repository \
  --output-dir /tmp/code-review-output

# Review only selected worktree files.
go run . \
  --repo-path /path/to/go/repository \
  --files internal/server.go,internal/server_test.go

# Review a downloaded PR patch and stage its repository for Go checks.
go run . \
  --diff-file /tmp/pull.patch \
  --repo-path /path/to/go/repository
```

The local runtime is a development fallback and fails closed unless explicitly
enabled:

```bash
go run . --fixture resource_leak --runtime local --allow-local
```

`--staticcheck` enables the optional staticcheck command. Build the supplied
Dockerfile and select it when staticcheck is required:

```bash
docker build -t trpc-agent-go-code-review:go1.24.4 docker
go run . --repo-path /path/to/go/repository --staticcheck \
  --container-image trpc-agent-go-code-review:go1.24.4
```

All runtime commands use a clean environment and `GOPROXY=off`.

## Public Fixtures

Every fixture produces `review_report.json`, `review_report.md`, and a SQLite
record:

```bash
for fixture in testdata/fixtures/*.diff; do
  name="$(basename "${fixture}" .diff)"
  go run . --fixture "${name}" --dry-run \
    --output-dir "/tmp/reviews/${name}"
done
```

Fixtures cover clean changes, command/SQL security, goroutine and context
lifecycle, resource closing, database rows and transactions, missing tests,
deduplication, sandbox failure, error handling, and sensitive data redaction.

## Persistence

The default database is `<output-dir>/reviews.db`. Its schema is initialized
automatically from [schema.sql](schema.sql). A different SQL backend can
implement the `ReviewStore` interface.

```bash
sqlite3 /tmp/code-review-output/reviews.db \
  'SELECT id, status, conclusion FROM review_tasks;'
sqlite3 /tmp/code-review-output/reviews.db \
  'SELECT command, action, risk FROM governance_decisions;'
sqlite3 /tmp/code-review-output/reviews.db \
  'SELECT severity, category, file_path, line_number FROM findings;'
```

See [DESIGN.md](DESIGN.md) for the architecture and security rationale.
Generated example reports are under [sample_output](sample_output).

## Validation

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
```
