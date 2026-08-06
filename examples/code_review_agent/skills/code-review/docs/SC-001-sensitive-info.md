# SC-001 — Sensitive Information In Logs

| Field | Value |
| --- | --- |
| **RuleID** | SC-001 |
| **Severity** | High |
| **Category** | Security |
| **Confidence** | 0.8 |

## Description

Detects sensitive identifiers (PII, credentials, tokens, emails) emitted to
logs via `log.Print*`, `fmt.Fprintln(os.Stderr, ...)` or structured loggers.
Logged secrets are captured by aggregation pipelines and can leak outside the
trust boundary.

> **Scope note**: This rule only scans **AddedLines** — lines prefixed with `+`
> in the unified diff. Unchanged context lines are ignored so the rule does not
> fire on pre-existing logging that predates the change under review.

## Evidence Example

```diff
+func login(user string, password string) {
+    log.Printf("login user=%s password=%s", user, password)
+    // ...
+}
```

## Recommendation

Redact sensitive fields before logging, or log only non-identifying metadata:

```go
func login(user string, password string) {
    log.Printf("login request_id=%s credential=***REDACTED***", requestID)
    // ...
}
```

## False Positive Notes

- Field names that merely resemble sensitive identifiers (e.g. `passwordHash`
  where only the hash is logged) may still match; verify the logged value is
  the raw secret before suppressing.
- Debug logs guarded by a build tag (`//go:build debug`) intended only for
  local development should still be reviewed — they can be enabled
  accidentally in production builds.
