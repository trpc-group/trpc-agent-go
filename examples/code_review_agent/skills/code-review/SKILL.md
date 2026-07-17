---
name: code-review
description: Deterministic Go code review workflow for diff, sandbox checks, and structured findings.
---

# Code Review Skill

Use this skill for Go pull request review. Read the diff, apply the rules in
`rules/`, and run `scripts/run_go_checks.sh` only after the host permission
policy allows sandbox execution.

Inputs are staged read-only. Write review artifacts to `out/`. Never print or
persist raw API keys, tokens, passwords, or secrets; redact them before output.

Required output fields for every finding are:

`severity`, `category`, `file`, `line`, `title`, `evidence`,
`recommendation`, `confidence`, `source`, and `rule_id`.

Low-confidence issues must be reported as warnings or human-review items, not
as high-confidence findings.
