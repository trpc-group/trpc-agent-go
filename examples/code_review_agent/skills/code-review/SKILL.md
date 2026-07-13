---
name: code-review
description: Review Go diffs and pull requests for security vulnerabilities, goroutine and resource leaks, error-handling mistakes, missing tests, sensitive info exposure, and database transaction issues. Use when the user asks to review Go code, audit a change, check a PR, find bugs in Go, or wants deterministic Go code review — even if they don't say "code review" explicitly.
---

# Code Review Skill

Review Go code changes against deterministic rules and produce structured findings.

## Inputs

- A unified diff, patch file path, or working-tree path.
- Optionally, the Go files or hunks to focus on.

## Severity scale

Use exactly one of these five levels in every finding:

| Level      | Meaning                                                       |
| ---------- | ------------------------------------------------------------- |
| `critical` | Vulnerable in production.                                     |
| `high`     | Strong risk signal — likely a real bug.                       |
| `medium`   | Real bug or hygiene issue.                                    |
| `low`      | Minor risk or code smell.                                     |
| `warning`  | Low-confidence or advisory finding. Routed to `warnings`.     |

`warning` is used when the signal is structurally noisy (e.g. missing test diff). Findings with `confidence < 0.70` are also routed to `warnings` even when their severity is `critical` / `high` / `medium` / `low`.

## Workflow

1. Read the diff. Identify added Go lines and the package each file belongs to.
2. For each rule in `references/rules.md`, evaluate the relevant lines against the rule's detection patterns. Check exemptions before emitting a finding.
3. Bucket each finding into `findings` (high confidence) or `warnings` (low confidence / advisory).
4. Write the report.

## Finding schema

Every finding conforms to:

```json
{
  "severity": "high",
  "category": "concurrency",
  "file": "internal/foo/bar.go",
  "line": 42,
  "title": "goroutine launched without visible cancellation path",
  "evidence": "go func() { ... }()",
  "recommendation": "Thread context cancellation into the goroutine and ensure it exits on ctx.Done().",
  "confidence": 0.78,
  "rule_id": "GO-CONTEXT-001",
  "needs_human_review": false
}
```

`category` is one of: `security`, `sensitive_info`, `concurrency`, `resource_lifecycle`, `error_handling`, `tests`, `database_lifecycle`.

## Output

A report object with:

- `findings` — high-confidence findings (severity ≠ `warning`, confidence ≥ 0.70).
- `warnings` — low-confidence or advisory findings.
- `summary` — one-line summary string.

See `assets/review_report.example.json` for a complete example.

## Rules

See `references/rules.md` for the full rule catalog with detection patterns, exemptions, evidence extraction, and recommendations.
