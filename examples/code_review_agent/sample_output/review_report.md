# Code Review Report

- Task: `cr-de50da8e-0a97-49d5-b106-b35c5e05a52e`
- Status: `completed`
- Summary: 2 high-confidence findings and 2 human-review items detected. Model review: Deterministic fake-model review completed.
- Findings: 2
- Needs human review: 2

## Severity Summary

- critical: 2

## Findings

### [critical] Potential hard-coded secret

- File: `security/secret.go:3`
- Rule: `SEC001`
- Category: `security`
- Confidence: 0.96
- Evidence: `const apiKey = [REDACTED_SECRET]`
- Recommendation: Move secrets to a secret manager or environment variable and rotate the exposed credential.

### [critical] Potential hard-coded secret

- File: `security/secret.go:4`
- Rule: `SEC001`
- Category: `security`
- Confidence: 0.96
- Evidence: `var password = [REDACTED_SECRET]`
- Recommendation: Move secrets to a secret manager or environment variable and rotate the exposed credential.


## Needs Human Review

### [low] Fake model flagged the first added line for demonstration

- File: `security/secret.go:3`
- Rule: `FAKE001`
- Category: `model_review`
- Confidence: 0.55
- Evidence: `const apiKey = [REDACTED_SECRET]`
- Recommendation: Replace fake-model mode with llm mode for real model commentary.

### [medium] Changed Go code without nearby test changes

- File: `security/secret.go:3`
- Rule: `TEST001`
- Category: `testing`
- Confidence: 0.58
- Evidence: `No _test.go file is present in the same diff.`
- Recommendation: Add or update tests that cover the changed behavior.


## Warnings

None.

## Permission Decisions

- `bash skills/code-review/scripts/diff_summary.sh -`: **allow** - command is allow-listed for code review checks
- `bash skills/code-review/scripts/secret_scan.sh -`: **allow** - command is allow-listed for code review checks
- `bash skills/code-review/scripts/go_static_checks.sh work/repo`: **allow** - command is allow-listed for code review checks

## Filter Decisions

- `SEC001` at `security/secret.go:3` (confidence): **keep** - confidence 0.96 >= 0.75 keeps the finding
- `TEST001` at `security/secret.go:3` (confidence): **needs_human_review** - confidence 0.58 in [0.45, 0.75) routes to human review
- `SEC001` at `security/secret.go:4` (confidence): **keep** - confidence 0.96 >= 0.75 keeps the finding
- `FAKE001` at `security/secret.go:3` (confidence): **needs_human_review** - confidence 0.55 in [0.45, 0.75) routes to human review
- `SEC001` at `security/secret.go:3` (confidence): **keep** - confidence 0.96 >= 0.75 keeps the finding
- `TEST001` at `security/secret.go:3` (confidence): **needs_human_review** - confidence 0.58 in [0.45, 0.75) routes to human review
- `SEC001` at `security/secret.go:4` (confidence): **keep** - confidence 0.96 >= 0.75 keeps the finding

## Sandbox Runs

- `bash skills/code-review/scripts/diff_summary.sh -`: skipped, exit=0, duration=0ms
  - error: dry-run/mock mode did not execute skill scripts
- `bash skills/code-review/scripts/secret_scan.sh -`: skipped, exit=0, duration=0ms
  - error: dry-run/mock mode did not execute skill scripts
- `bash skills/code-review/scripts/go_static_checks.sh work/repo`: skipped, exit=0, duration=0ms
  - error: no --repo-path provided; static checks need a repository

## Metrics

- total duration: 593ms
- sandbox duration: 0ms
- model duration: 1ms
- tool calls: 0
- model calls: 1
- permission denies: 0
- permission intercepts: 0
- blocked commands: 0
- skipped commands: 3
- warnings: 0
- needs human review: 2
