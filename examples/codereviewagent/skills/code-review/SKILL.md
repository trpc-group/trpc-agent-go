---
name: code-review
description: Review Go diffs with deterministic security, concurrency, lifecycle, database, test, and secret-handling rules.
languages: go
---

# Governed Go Code Review

Route added Go lines through the following rules. Report only findings supported by the changed lines. Findings below confidence 0.80 must use `needs_human_review` instead of an actionable status.

- `rule_id: SEC001` — detect credential-like values and redact them before persistence.
- `rule_id: SEC002` — flag shell command construction at an injection boundary.
- `rule_id: CON001` — require cancellation and join ownership for new goroutines.
- `rule_id: CON002` — reject request work detached with `context.Background`.
- `rule_id: DB001` — require explicit database connection ownership and shutdown.
- `rule_id: RES001` — require immediate cleanup for files and HTTP responses.
- `rule_id: TST001` — request human review when production changes lack test changes.
- `rule_id: SAN001` — preserve sandbox failures as review evidence without aborting.

Deduplicate by file, line, and rule ID. Every finding must contain severity, category, confidence, source, rule ID, status, explanation, and an executable remediation. Never persist raw credentials, environment secrets, or unrestricted sandbox output.
