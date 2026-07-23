---
name: code-review
description: Automated Go code review skill with static analysis rules and sandbox execution.
---

# Code Review Skill

## Overview

This skill provides automated code review for Go projects. It analyzes
git diffs, applies static analysis rules, optionally runs sandbox
checks (go vet, go test), and produces structured findings with
severity levels, evidence, and recommendations.

## When to Use

Use this skill when you need to:

1. Review a git diff or PR patch for code quality and security issues
2. Detect goroutine leaks, resource leaks, SQL injection, and other
   common Go pitfalls
3. Run sandbox-based checks (go vet, go test) on changed code
4. Generate structured review reports with findings, warnings, and
   monitoring metrics
5. Persist review results to a SQLite database for audit and tracking

## Rules

This skill covers the following rule categories:

- **Security**: SQL injection, command injection, hardcoded secrets
- **Goroutine leaks**: goroutines without context, context not passed
- **Resource leaks**: unclosed files, HTTP response bodies, DB connections
- **Error handling**: ignored errors, panic in goroutines
- **DB lifecycle**: missing defer Close, missing transaction rollback
- **Sensitive info**: secrets logged, credentials in code
- **Test coverage**: exported functions without tests

## Usage

### Basic (dry-run, rules only)

```bash
go run . --diff-file path/to/changes.diff --dry-run
```

### With sandbox execution

```bash
go run . --diff-file path/to/changes.diff --repo-path ./myproject --dry-run=false
```

### Custom database and output

```bash
go run . --diff-file changes.diff --db-path ./reviews.db --output-dir ./reports
```

## Sandbox Scripts

The `scripts/` directory contains helper scripts:

- `run_checks.sh`: Runs `go vet` and `go test` on the repository
- `parse_diff.sh`: Parses a diff file and extracts changed file list

## Output

The skill generates:

1. `review_report.json`: Structured JSON report with all findings
2. `review_report.md`: Human-readable Markdown report
3. SQLite database with tables: review_tasks, findings, sandbox_runs,
   permission_decisions, artifacts, monitoring_summary, review_reports

## Safety

- High-risk commands (rm, curl, wget) are denied by the permission
  policy
- Commands needing review (docker, git push) are blocked pending
  human approval
- Production execution defaults to the network-disabled, unprivileged
  `codeexecutor/container` workspace runtime; local execution is an explicit
  development fallback
- Sandbox execution has timeout (30s default), output size limits (1MB
  default), clean environment, and CPU/memory/PID/workspace limits
- Sensitive information in findings is automatically redacted
