# RL-001 — Resource Not Closed

| Field | Value |
| --- | --- |
| **RuleID** | RL-001 |
| **Severity** | High |
| **Category** | Reliability |
| **Confidence** | 0.9 |

## Description

Flags `*os.File`, `io.Closer` and `http.Response.Body` values obtained from
`os.Open`, `os.Create`, `os.OpenFile` and `http.Get`/`http.Post` that are not
closed via a `defer` statement. Unclosed resources leak file descriptors and
eventually trigger `EMFILE` under load.

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

- Resources handed off to a wrapper that owns their lifecycle (e.g.
  `bufio.NewReader` wrapping a file that is closed by the reader) may false
  positive; verify ownership transfer before suppressing.
- Files opened in `main` and intentionally closed at process exit are rare;
  prefer `defer` for consistency.
