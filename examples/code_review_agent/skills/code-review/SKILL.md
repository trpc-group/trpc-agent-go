---
name: code-review
description: Review Go diffs with deterministic safety, lifecycle, concurrency, error-handling, database, and test rules before running approved sandbox checks.
---

# Go code review

1. Parse only changed lines from a bounded unified diff or a controlled repository snapshot.
2. Apply the rules in `docs/go-review-rules.md`; retain the file, new-file line, evidence, confidence, and remediation.
3. Redact credentials before staging, persistence, logs, or reports.
4. Ask the governance policy before every command. A deny or ask decision is an audit result, never permission to execute.
5. Run the audited statistics script and optional Go checks inside an isolated workspace with a clean environment, timeout, and bounded output.
6. Deduplicate by file, line, and category. Route uncertain observations to human review.
7. Persist the task, input digest, decisions, runs, findings, artifacts, metrics, and final report.

Do not claim that a failed or unavailable sandbox check passed. Do not infer that absence of a deterministic finding proves the change is safe.
