---
name: code-review
description: Go code review skill with diff parsing, static checks, and rule scripts.
---

# Code Review Skill

Deterministic Go code review helpers for tRPC-Agent-Go CR Agent.

## Workflow

1. Stage the unified diff at `work/inputs/changes.diff`.
2. Load rules from `docs/rules.md` when needed.
3. Run sandbox checks via `scripts/run_checks.sh`.
4. Optionally run `go vet` / `go test` when a Go module workspace is staged under `work/repo/`.

## Commands

Parse diff and run lightweight checks (required):

```bash
bash scripts/run_checks.sh work/inputs/changes.diff
```

When a repository checkout is available:

```bash
cd work/repo && go vet ./...
cd work/repo && go test ./...
```

## Output

- Exit `0`: checks passed
- Exit `2`: checks failed (ignored errors, invalid diff, vet/test failure) — recorded as sandbox failure, review continues
- Non-zero other codes: unexpected script error

## Safety

High-risk commands (`rm -rf`, `curl|bash`, `git push`) must pass PermissionPolicy before execution. Denied commands are logged but not executed.
