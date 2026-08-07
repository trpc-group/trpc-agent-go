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
- `bash scripts/run_checks.sh test` for `go test ./...` in each affected Go
  module declared by the staged repository manifest, only when the caller
  explicitly opts in with `--skip-go-test=false` for trusted code or a runtime
  whose outbound-network isolation has been independently verified. This
  example does not claim that its E2B configuration provides that boundary.
- `bash scripts/run_checks.sh vet` for `go vet ./...` in each affected Go
  module.
- `bash scripts/run_checks.sh staticcheck` for each affected Go module only
  when explicitly enabled.

Repository checks consume `.trpc-agent-review-modules` from the staged snapshot
root. It contains sorted, repository-relative module directories separated by
NUL bytes, with `.` representing the root module. Missing, empty, absolute, or
parent-escaping entries must fail closed.

Rules prioritize security issues, goroutine and context lifecycle risks,
resource leaks, ignored errors, database lifecycle problems, and missing tests.
Every command must pass the private command gate and the per-run permission
policy before execution. The default go test skip is recorded without invoking
either gate or creating a sandbox.

Outputs should be treated as advisory sandbox evidence. Deterministic diff and
AST rules remain the primary source of findings, and low-confidence or blocked
governance events must be routed to warnings that require human review.
Malformed or incomplete diffs must also require human review and cannot produce
a pass conclusion.
