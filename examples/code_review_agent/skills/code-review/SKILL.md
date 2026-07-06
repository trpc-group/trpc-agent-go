---
name: code-review
description: Review Go project diffs, PR patches, and local workspace changes with deterministic rules, model-planned sandbox execution, permission/safety governance, durable audit persistence, and JSON/Markdown reports. Use when Codex needs to inspect code changes for security leaks, goroutine/context lifecycle issues, resource leaks, ignored errors, database transaction lifecycle problems, missing tests, or when a code-review agent must emit structured findings with auditable evidence.
---

# Code Review

Review Go diffs with a safety-first workflow that combines deterministic checks,
model orchestration, sandbox command governance, secret redaction, durable audit
records, and structured report generation.

This skill is the policy layer for the example code-review agent. Keep review
output evidence-based: every finding should point to a changed file, added line,
rule id, severity, confidence, source, status, and concrete recommendation. If
the evidence is plausible but not strong enough, route it to `warning` or
`needs_human_review` instead of overstating certainty.

## Workflow

1. Parse the unified diff into changed files, hunks, added line numbers, and Go
   package directories.
2. Read `docs/rules.md` before changing review behavior, planning sandbox
   commands, or writing new findings.
3. Run deterministic checks first; prefer them when the added lines provide
   direct evidence.
4. Use model planning only to coordinate allowed commands and rule sources. Do
   not let model output bypass permission, safety, redaction, or persistence.
5. Pass every planned command through permission and safety checks before
   sandbox execution.
6. Execute allowed commands in the selected workspace runtime; record denied,
   skipped, failed, or unavailable runs instead of silently falling back.
7. Redact secrets before persisting findings, sandbox output, reports, metrics,
   or audit records.
8. Generate both `review_report.json` and `review_report.md` from the same
   structured review result.

## Bundled Resources

- `docs/rules.md`: canonical rule ids, categories, detection direction, and
  confidence routing. Load it before changing or extending review rules.
- `scripts/diff_summary.go`: deterministic helper that parses unified diffs and
  returns changed-file count, added/deleted line counts, and sorted file names.
- `scripts/go_checks.go`: deterministic helper that parses unified diffs and
  returns Go review findings by reusing the tested internal parser and rules
  engine.

Keep core parsing, rule evaluation, redaction, deduplication, and persistence in
tested Go packages. Let Skill scripts call those packages rather than duplicating
logic inside the Skill folder.

## Finding Policy

- Use `finding` for direct, high-confidence evidence in added lines.
- Use `warning` for low-confidence or advisory issues, including missing test
  coverage hints.
- Use `needs_human_review` for high-risk but ambiguous issues and governance
  decisions that block useful review context.
- Deduplicate findings by stable fingerprint so repeated evidence does not
  inflate the report.
- Preserve exact evidence after redaction; never store raw credentials, tokens,
  private keys, JWTs, or password-like values.

## Sandbox Policy

Production-oriented runtimes are `container` and `e2b`. `local` is an explicit
development fallback only. Tests should use `fake` so they do not depend on
Docker, E2B, API keys, network availability, or host-specific tools.

Non-fake runs require model orchestration configuration through `MODEL` or
`--model` plus `OPENAI_API_KEY`; `OPENAI_BASE_URL` may point at an
OpenAI-compatible endpoint. If model configuration, model planning, or runtime
initialization fails, return an English error and persist the failure instead of
silently falling back.

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

## Report Requirements

Reports should include:

- review task id, status, input type, diff hash, timestamps, and error text
- non-secret model plan metadata
- changed files, hunks, and added-line evidence
- findings with severity, category, source, rule id, status, confidence, and
  recommendation
- permission decisions and sandbox run records
- generated artifact paths and checksums
- metrics for finding count, severity distribution, blocked permissions,
  sandbox duration, error distribution, and redaction count

Keep user-facing report text, CLI output, error messages, and persisted audit
text in English for stable evaluation.
