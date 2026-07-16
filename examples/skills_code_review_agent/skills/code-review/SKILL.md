---
name: code-review
description: Deterministic Go code review workflow with sandbox checks, permission decisions, structured findings, and report artifacts.
---

Overview

Use this skill to review Go pull requests from a unified diff, PR patch,
or repository working tree. The workflow focuses on engineering risks
that are common in Go services: security mistakes, goroutine lifecycle
leaks, context propagation, resource closing, error handling, database
transaction lifecycle, and missing tests.

The host agent loads this skill before reviewing. It then parses the
diff, runs deterministic line and AST-assisted rules, asks the
PermissionPolicy before any sandbox command, stages this skill into the
container or E2B workspace, executes audited scripts and Go checks,
parses tool diagnostics back into structured findings, stores all
decisions and findings in SQLite, and writes `review_report.json` plus
`review_report.md`.

Rules

- Read `docs/rules.md` for the audited rule catalog.
- High-confidence findings must include severity, category, file, line,
  title, evidence, recommendation, confidence, source, and rule_id.
- Low-confidence or environment-dependent items must be warnings or
  needs_human_review.
- Never include raw API keys, tokens, passwords, Authorization headers,
  or private keys in reports or database records.

Sandbox Commands

These commands are expected to run from the sandbox-staged repository:

1) Go tests

   Command:

   go test ./...

2) Go vet

   Command:

   go vet ./...

3) Staticcheck when available

   Command:

   staticcheck ./...

Helper Scripts

1) Parse a diff summary into JSON:

   bash scripts/diff_summary.sh work/change.diff out/diff_summary.json

2) Verify a report does not contain obvious secrets:

   bash scripts/check_redaction.sh out/review_report.json

Output Files

- out/diff_summary.json
- out/review_report.json
- out/review_report.md
