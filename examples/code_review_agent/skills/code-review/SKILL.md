---
name: code-review
description: Automated code review agent for Go projects. Analyzes diff files, checks for security issues, goroutine leaks, resource leaks, error handling, test coverage, and database lifecycle issues.
---

Overview

The Code Review Skill provides automated code review for Go projects.
It reads a unified diff, categorizes changes by file, runs static analysis
rules, and returns structured findings with severity levels, evidence,
and actionable fix recommendations.

Rules

The skill covers the following rule categories:

1. Security: SQL injection, command injection, hardcoded credentials
2. Goroutine/Context Leak: goroutines without ctx.Done guards, missing defer cancel
3. Resource Leak: os.Open / http.Get / db.Query without defer Close
4. Error Handling: silently ignored errors, missing error returns, recover() misuse
5. Test Coverage: exported symbols without corresponding test functions
6. Database Lifecycle: missing deferred Rollback, rows.Err() checks

Usage

1. Load the skill:
   skill_load code-review

2. Run a review with a diff file:
   workspace_exec -- command="cat diff.patch | go run main.go --diff-file -"

3. Or use the CLI directly:
   cd skills/code-review
   go run ../../main.go --diff-file <path> [--dry-run] [--output <dir>]

Scripts

- scripts/check_security.sh:  Security pattern scanning (gosec wrapper)
- scripts/check_leak.sh:      Goroutine/context leak detection
- scripts/check_resource.sh:  Resource leak detection
- scripts/check_error.sh:     Error handling pattern checks
- scripts/check_test.sh:      Test coverage analysis
- scripts/check_db.sh:        Database lifecycle checks

Output

- review_report.json: Structured JSON report for programmatic consumption
- review_report.md:  Human-readable Markdown report
