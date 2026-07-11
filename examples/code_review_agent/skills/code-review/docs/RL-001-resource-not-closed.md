# RL-001 — Resource Not Closed

| Field | Value |
| --- | --- |
| **RuleID** | RL-001 |
| **Severity** | High |
| **Category** | Reliability |
| **Confidence** | 0.9 |

## Description

Flags `*os.File` values obtained from `os.Open`, `os.Create` and
`os.OpenFile` that are not closed via a `defer` statement. Unclosed file
handles leak file descriptors and eventually trigger `EMFILE` under load.

> **Scope note**: The current evaluator matches `os.Open`/`os.Create`/
> `os.OpenFile` only. `http.Get`/`http.Post` response bodies and generic
> `io.Closer` values are not yet detected.

## Evidence Example

```go
func readConfig(path string) ([]byte, error) {
    f, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    // f.Close() is never called — leaks a file descriptor per call.
    return io.ReadAll(f)
}
```

## Recommendation

Defer the close immediately after a successful open:

```go
func readConfig(path string) ([]byte, error) {
    f, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    defer f.Close()
    return io.ReadAll(f)
}
```

## False Positive Notes

- Resources wrapped in a type that takes ownership of the underlying `*os.File`
  (e.g. a custom `ReadCloser` that closes the file in its own `Close` method)
  may still be flagged because the evaluator matches `os.Open` at the call
  site, not the ownership transfer. Verify the wrapper closes the file before
  suppressing.
- `bufio.NewReader` does **not** close the underlying file — it is not an
  ownership transfer. The caller must still `defer f.Close()`.
- Files opened in `main` and intentionally closed at process exit are rare;
  prefer `defer` for consistency.
