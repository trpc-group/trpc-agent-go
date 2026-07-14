# Code Review Report

- Summary: 2 high-confidence findings and 1 human-review items detected.
- Findings: 2
- Needs human review: 1

## Findings

### [critical] Potential hard-coded secret

- File: `security/secret.go:3`
- Rule: `SEC001`
- Evidence: `const apiKey = [REDACTED_SECRET]`
- Recommendation: Move secrets to a secret manager or environment variable and rotate the exposed credential.

### [critical] Potential hard-coded secret

- File: `security/secret.go:4`
- Rule: `SEC001`
- Evidence: `var password = [REDACTED_SECRET]`
- Recommendation: Move secrets to a secret manager or environment variable and rotate the exposed credential.

## Needs Human Review

### [medium] Changed Go code without nearby test changes

- File: `security/secret.go:3`
- Rule: `TEST001`
