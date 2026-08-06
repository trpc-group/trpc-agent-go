# EH-001 — Unchecked Error

| Field | Value |
| --- | --- |
| **RuleID** | EH-001 |
| **Severity** | Medium |
| **Category** | Correctness |
| **Confidence** | 0.75 |

## Description

Detects function calls whose returned `error` value is discarded (assigned to
`_` or ignored entirely). Silently dropped errors hide real failures and make
debugging extremely difficult.

## Evidence Example

```go
func writeCache(b []byte) {
    os.WriteFile("/tmp/cache", b, 0o644) // error discarded
    fmt.Fprintln(os.Stderr, "cache written")
}
```

## Recommendation

Handle or propagate the error explicitly:

```go
func writeCache(b []byte) error {
    if err := os.WriteFile("/tmp/cache", b, 0o644); err != nil {
        return fmt.Errorf("write cache: %w", err)
    }
    return nil
}
```

## False Positive Notes

- Functions documented to never return a non-nil error in practice (e.g.
  `fmt.Println` writing to `os.Stdout`) commonly discard the error by
  convention; the rule still flags them. Suppress with an explicit comment or
  `// code-review:ignore EH-001`.
- Cleanup paths where the error is genuinely uninteresting (e.g. closing a
  file during shutdown) may use `_ = f.Close()` to make the intent explicit.
