# Code Review Agent

An automated code review agent built on tRPC-Agent-Go, combining Skills, sandbox execution, database storage, and governance policies.

## Quick Start

```bash
# Analyze a diff file
go run main.go --diff-file testdata/diffs/01_clean_diff.diff

# Dry-run mode (no sandbox, deterministic rules only)
go run main.go --diff-file testdata/diffs/02_security_issue.diff --dry-run

# Custom output directory
go run main.go --diff-file testdata/diffs/03_goroutine_leak.diff --output ./my_review
```

## Requirements

- Go 1.23+
- CGO enabled (for SQLite storage)

## Architecture

```
Input (diff file / repo path)
    │
    ▼
Diff Parser ──→ ChangedFile list with hunks, line numbers, Go packages
    │
    ▼
CR Rules (10 rules across 6 categories)
    ├── Security (SQL injection, command injection, hardcoded keys)
    ├── Goroutine/Context Leak
    ├── Resource Leak
    ├── Error Handling
    ├── Test Coverage
    └── DB Lifecycle
    │
    ▼
DedupEngine ──→ Remove same-file same-line same-rule duplicates
    │
    ▼
Sanitizer ──→ Redact API keys, tokens, private keys
    │
    ▼
Report Generator ──→ review_report.json + review_report.md
    │
    ▼
SQLite Store ──→ Tasks, Findings, Sandbox Runs, Decisions, Reports
```

## CLI Reference

| Flag | Default | Description |
|------|---------|-------------|
| `--diff-file` | `""` | Path to a unified diff file |
| `--repo-path` | `""` | Path to a git repository (WIP) |
| `--dry-run` | `false` | Deterministic rules only, no sandbox |
| `--output` | `./review_output` | Output directory for reports |
| `--verbose` | `false` | Enable verbose logging |

## Output

- `review_report.json` — structured JSON for programmatic use
- `review_report.md` — human-readable Markdown report

## Project Structure

```
├── main.go                        CLI entry point
├── internal/
│   ├── diff/                      Unified diff parser
│   ├── finding/                   Core types, dedup, sanitizer
│   ├── report/                    JSON/Markdown report generation
│   ├── runner/                    Agent orchestration, rules, sandbox, permissions
│   │   └── rules/                 10 built-in code review rules
│   └── storage/                   SQLite store implementation
├── skills/code-review/            CR Skill (SKILL.md, rules, scripts)
├── testdata/diffs/                9 test diff samples
└── docs/                          Design document
```

## Development

```bash
go build ./...
go test ./... -cover
golangci-lint run ./...
```

## License

Apache 2.0
