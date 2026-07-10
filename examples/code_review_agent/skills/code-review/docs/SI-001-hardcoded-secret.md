# SI-001 — Hardcoded Secret

| Field | Value |
| --- | --- |
| **RuleID** | SI-001 |
| **Severity** | High |
| **Category** | Security |
| **Confidence** | 0.85 |

## Description

Detects hardcoded secrets, API keys, tokens and passwords embedded directly in
source code. Secrets committed to version control are a leading cause of
credential leakage and must never appear in source files.

## Evidence Example

```go
const apiKey = "sk-1234567890abcdef1234567890abcdef"

func newClient() *http.Client {
    cfg := &Config{Token: "ghp_abcdef0123456789abcdef0123456789abcd"}
    return cfg.Client()
}
```

## Recommendation

Load secrets from the environment or a secrets manager at runtime:

```go
func newClient() (*http.Client, error) {
    apiKey := os.Getenv("API_KEY")
    if apiKey == "" {
        return nil, errors.New("API_KEY env var is required")
    }
    cfg := &Config{Token: apiKey}
    return cfg.Client(), nil
}
```

## False Positive Notes

- High-entropy strings that are not secrets (e.g. commit SHAs, generated UUIDs,
  fixture data) may match. The rule prefers named identifiers (`apiKey`,
  `token`, `secret`, `password`) to reduce noise.
- Test fixtures that intentionally embed dummy tokens should be excluded via
  the `// code-review:ignore SI-001` pragma or a path allow-list.
