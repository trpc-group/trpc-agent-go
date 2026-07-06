# Code Review

Review Go diffs for security, lifecycle, concurrency, error-handling, and test
coverage risks. Load `docs/rules.md` before planning sandbox commands or
findings. Prefer deterministic rule output when evidence is clear, and mark
low-confidence or governance-blocked items as warnings or needs-human-review
instead of high-confidence findings.

## Audit Requirements

Record every planned command with:

- framework permission action
- original safety decision
- risk level
- rule id
- reason
- blocked status

Never store raw secrets in findings, reports, sandbox output, or audit records.
Replace suspected secrets with `[REDACTED_SECRET]`.
