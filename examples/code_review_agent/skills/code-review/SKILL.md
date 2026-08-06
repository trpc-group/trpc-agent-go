---
name: code-review
description: >
  Review Go code changes from a unified diff or git working tree.
  Run sandboxed checks, emit structured findings, never print secrets.
---

# Code Review Skill

Use this skill when reviewing Go diffs, PR patches, or local git changes.

## Workflow

1. Load this skill and read `docs/rules.md`.
2. Place the redacted diff at `$REVIEW_DIFF_PATH`.
3. Run `scripts/run_checks.sh` inside the workspace (sandbox).
4. Aggregate JSON findings and write reports under `out/`.
5. Never print raw API keys, tokens, or passwords.

## Safety

- High-risk commands must pass PermissionPolicy (`allow` only).
- Prefer container/e2b executors; local is development fallback only.
- Respect timeout and output size limits.
