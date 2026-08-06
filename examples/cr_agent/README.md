# CR Agent — Automated Code Review Agent

An automated code review (CR) agent built on the tRPC-Agent-Go framework.
It reads unified diffs, file paths, or git working-tree changes, runs a
static-analysis rule engine, optionally executes `go vet` and `go test`
in a sandboxed environment, deduplicates findings, and writes structured
review reports (JSON + Markdown) to disk. All review tasks, findings, and
sandbox runs are persisted to SQLite for later querying, monitoring, and
replay.

## Architecture

```
┌─────────────┐    ┌──────────┐    ┌───────────┐    ┌──────────┐
│  Input      │───▶│ Diff     │───▶│ Rule      │───▶│ Dedup &  │
│  (diff/git/ │    │ Parser   │    │ Engine    │    │ Demote   │
│   files)    │    │          │    │ (13 rules)│    │          │
└─────────────┘    └──────────┘    └───────────┘    └────┬─────┘
                                                         │
┌─────────────┐    ┌──────────┐                         ▼
│  Sandbox    │───▶│ Audit    │    ┌───────────┐    ┌──────────┐
│  (vet/test) │    │ Log      │───▶│ Report    │───▶│ SQLite   │
│  Permission │    │          │    │ (JSON+MD) │    │ Store    │
│  Policy     │    │          │    │           │    │          │
└─────────────┘    └──────────┘    └───────────┘    └──────────┘
```

### Packages

| Package | Responsibility |
|---------|---------------|
| `internal/diff` | Unified diff parser producing structured `FileChange` records |
| `internal/rules` | 13 static-analysis rules across 7 categories |
| `internal/sandbox` | Permission policy (deny-by-default) and local command executor |
| `internal/dedup` | Finding deduplication and low-confidence demotion |
| `internal/report` | JSON and Markdown report generation |
| `internal/storage` | Store interface for persistence |
| `internal/storage/sqlite` | SQLite implementation of the Store interface |
| `internal/types` | Shared data structures: `Finding`, `ReviewTask`, `ReviewReport` |
| `internal/pipeline` | Orchestrates the end-to-end review flow |
| `skills/code-review` | CR Skill definition, rule docs, and helper scripts |

## Quick Start

```bash
cd examples/cr_agent

# Run against the built-in test fixture
go run . -fixture -no-sandbox

# Review a diff file
go run . -diff-file path/to/changes.patch

# Review a git commit range
go run . -repo-path /path/to/repo -commit-range HEAD~3..HEAD

# Review specific files
go run . -files file1.go,file2.go
```

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-fixture` | false | Run against the built-in test fixture |
| `-diff-file` | | Path to a unified diff file |
| `-repo-path` | | Path to a git repository |
| `-commit-range` | `HEAD~1..HEAD` | Git revision range (with `-repo-path`) |
| `-files` | | Comma-separated file paths |
| `-no-sandbox` | false | Disable sandbox execution |
| `-timeout` | 60s | Per-command sandbox timeout |
| `-db` | `cr_agent.db` | SQLite database path |
| `-output-dir` | `.` | Report output directory |
| `-confidence-threshold` | 0.5 | Findings below this are demoted to warnings |

## Rule Categories

The agent ships 13 rules across 7 categories:

| Rule ID | Category | Severity | Description |
|---------|----------|----------|-------------|
| SEC-001 | Security | Critical | SQL query built via string concatenation |
| SEC-002 | Security | Critical | Hardcoded secret or credential |
| SEC-003 | Security | High | Command injection via `exec.Command` concatenation |
| GOR-001 | Goroutine Leak | High | Goroutine without visible exit condition |
| GOR-002 | Goroutine Leak | Medium | `context.Background()` in long-running path |
| GOR-003 | Goroutine Leak | Medium | Channel send without select/default |
| RES-001 | Resource Close | High | Missing or removed `defer Close()` |
| RES-002 | Resource Close | High | Unclosed HTTP response body |
| ERR-001 | Error Handling | Medium | Ignored error return value |
| ERR-002 | Error Handling | Low | Variable `err` shadowing |
| TEST-001 | Missing Test | Low | New exported function without test |
| SENS-001 | Sensitive Leak | High | Credentials written to logs |
| DB-001 | DB Lifecycle | High | Transaction without `defer Rollback()` |

## Test Fixture

The `fixtures/sample_diff.patch` file contains a multi-file diff that
triggers 6 of the 13 rules:

- `internal/db/query.go` — SEC-001 (SQL injection)
- `internal/worker/loop.go` — GOR-001 (goroutine leak)
- `internal/storage/file.go` — RES-001 (removed defer Close)
- `internal/util/config.go` — ERR-001 (ignored error)
- `internal/auth/credentials.go` — SEC-002 (hardcoded secret)
- `internal/db/tx.go` — DB-001 (missing defer Rollback)

### Sample Output

Running `go run . -fixture -no-sandbox` produces:

```
Code Review Complete
  Findings:    6
    Critical:  2
    High:      3
    Medium:    1
  Files:       6
  Rules:       78 evaluated
  Reports:
    JSON: review_report.json
    MD:   review_report.md
  Database:    cr_agent.db
```

## Sandbox Security

The sandbox uses a **deny-by-default** permission policy:

- **Allowlist**: `go`, `cat`, `echo`, `grep`, `find`, `ls`, `head`, `tail`,
  `wc`, `diff`, `git`
- **Denylist**: `rm`, `curl`, `wget`, `ssh`, `sudo`, `chmod`, `kill`, etc.
- Denylist takes precedence over allowlist
- Environment variables are filtered to a safe allowlist (`PATH`, `HOME`,
  `GOROOT`, `GOPATH`, etc.)
- Output is truncated to 64 KB per stream
- Commands have a configurable timeout (default 60s)

## CR Skill

The `skills/code-review/` directory contains the Skill definition:

- `SKILL.md` — Skill manifest with usage instructions
- `rules/*.md` — Human-readable rule specifications per category
- `scripts/parse_diff.py` — Diff parser for use by the agent
- `scripts/run_go_vet.sh` — Wrapper for `go vet`
- `scripts/run_go_test.sh` — Wrapper for `go test`

## Testing

```bash
# Run all tests
go test ./internal/...

# Run with verbose output
go test -v ./internal/...

# Run a specific package
go test -v ./internal/rules/
```

The test suite includes 39 test cases covering:
- Diff parsing (5 tests)
- Rule engine — each rule with positive and negative cases (14 tests)
- Deduplication logic (5 tests)
- Sandbox permission policy and command parsing (15 tests)

## License

Tencent Apache 2.0. See `CONTRIBUTING.md` in the repository root.
