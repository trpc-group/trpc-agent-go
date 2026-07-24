# RL-001 - Resource Opened Without Deferred Close

**Severity:** high  
**Category:** resource_leak

## Description

A file, HTTP response, network connection, or SQL handle is opened but no
`defer` close is visible within five lines. Unclosed resources cause file
descriptor exhaustion and connection pool starvation.

## Detection

Triggered when any of these appear on an added line without a `defer` in the
next five lines of the hunk:

- `os.Open`, `os.Create`, `os.OpenFile`
- `http.Get`
- `net.Dial`
- `sql.Open`

## Fix

```go
f, err := os.Open(name)
if err != nil {
    return err
}
defer f.Close()
```
