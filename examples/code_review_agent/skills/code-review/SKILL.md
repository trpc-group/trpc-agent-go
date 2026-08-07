---
name: code-review
description: Automated Go code review skill. Analyzes diffs for security issues, goroutine leaks, resource lifecycle problems, database connection management, test coverage, and sensitive information exposure.
---

# Code Review Skill

This skill provides deterministic, rule-based code review for Go projects.
It does not require a model API key and can run in dry-run mode.

## Overview

The code-review skill:
1. Parses unified diffs or repository snapshots
2. Runs deterministic rules for common Go issues
3. Executes go vet and go test in a sandboxed environment
4. Generates structured findings with severity, evidence, and recommendations
5. Persists all data to SQLite for audit and query

## Usage

```bash
# Review a diff file
go run . --diff-file fixtures/diffs/security.diff

# Review a repository
go run . --repo-path /path/to/go/project

# Dry run without sandbox execution
go run . --diff-file fixtures/diffs/security.diff --dry-run
```

## Rule Categories

| Category | Rule ID | Description |
|----------|---------|-------------|
| Security | GO-SECURITY-001 | Command injection via unsanitized exec.Command |
| Secret | GO-SECRET-001 | Hardcoded credentials, tokens, API keys |
| Goroutine | GO-GOROUTINE-001 | Goroutine without context propagation |
| Resource | GO-RESOURCE-001 | File opened without defer close |
| Resource | GO-RESOURCE-002 | HTTP response body not closed |
| Database | GO-DB-001 | sql.Open without connection lifecycle management |
| Error | GO-ERROR-001 | Error not checked (blank identifier) |
| Error | GO-ERROR-002 | Bare panic in production code |
| Testing | GO-TEST-001 | New functions without corresponding tests |

## Sandbox Execution

In sandbox mode, the skill runs `go vet` and `go test` in an isolated
container. The local runtime is available only for development fallback.

## Governance

All sandbox commands go through a fail-closed permission policy. Denied
commands are recorded and never executed.
