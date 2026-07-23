---
name: code-review
description: Deterministic Go code review checks with governed sandbox commands.
---

# Code Review Skill

Use this skill for Go code review tasks driven by a unified diff or a staged
repository snapshot. The agent owns command selection; callers must not provide
arbitrary shell commands or arguments.

Allowed checks:

- `go version` to confirm the toolchain.
- `bash scripts/run_checks.sh test` for `go test ./...`.
- `bash scripts/run_checks.sh vet` for `go vet ./...`.
- `bash scripts/run_checks.sh staticcheck` only when explicitly enabled.

Rules prioritize security issues, goroutine and context lifecycle risks,
resource leaks, ignored errors, database lifecycle problems, and missing tests.
Every command must pass the private command gate and the per-run permission
policy before execution.

Outputs should be treated as advisory sandbox evidence. Deterministic diff and
AST rules remain the primary source of findings, and low-confidence or blocked
governance events must be routed to warnings that require human review.
