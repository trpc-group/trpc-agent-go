# code-review Skill

Automated code review for Go projects. Parses a unified diff, applies static
analysis rules, optionally runs `go vet` in a sandbox, and produces structured
findings with a JSON + Markdown report.

## Invocation

```bash
# Dry-run (no sandbox, rule-only)
go run ./examples/skills_code_review_agent \
  --diff-file=my.patch --dry-run --out=/tmp/review

# Full mode (runs go vet locally on changed packages — not sandboxed)
go run ./examples/skills_code_review_agent \
  --repo-path=/path/to/repo --out=/tmp/review
```

## Rules

| Rule ID | Category        | Severity |
|---------|-----------------|----------|
| GL-001  | goroutine_leak  | high     |
| RL-001  | resource_leak   | high     |
| EH-001  | error_handling  | medium   |
| SI-001  | sensitive_info  | high     |
| SQL-001 | sql_injection   | high     |
| CMD-001 | cmd_injection   | high     |

See `rules/` subdirectory for detailed descriptions.

## Output

- `review_report.json` - structured findings with metrics
- `review_report.md`  - human-readable Markdown report
- SQLite database (`review.db` by default) with `review_task`, `finding`,
  `sandbox_run`, and `report` tables queryable by task ID.

## Security Boundaries

- Full mode runs `go vet` locally on the host; it is **not sandboxed**. Only
  run it against repos you trust, or wire the sandbox path through a container
  runtime for untrusted input.
- `go vet` runs are time-limited (60 s per package).
- Output is capped at 4 096 bytes per run to prevent exfiltration via stdout.
- Sensitive values detected in diffs are redacted before storage.
