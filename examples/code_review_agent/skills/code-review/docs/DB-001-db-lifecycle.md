# DB-001 — DB Lifecycle

| Field | Value |
| --- | --- |
| **RuleID** | DB-001 |
| **Severity** | High |
| **Category** | Reliability |
| **Confidence** | 0.85 |

## Description

Flags `sql.Open` / `sql.NewConnector` / `gorm.Open` invocations whose returned
`*sql.DB` (or equivalent) handle is not closed via `defer db.Close()` or
wrapped in a lifecycle manager that owns shutdown. Leaked DB handles exhaust
connection pools and hold server-side resources.

## Evidence Example

```go
func newDB(dsn string) (*sql.DB, error) {
    db, err := sql.Open("sqlite3", dsn)
    if err != nil {
        return nil, err
    }
    // No defer db.Close() in the constructor, and the caller
    // is not documented to own the handle.
    db.SetMaxOpenConns(10)
    return db, nil
}
```

## Recommendation

Either defer close at the call site that owns the lifecycle, or document the
ownership transfer explicitly:

```go
func run(dsn string) error {
    db, err := sql.Open("sqlite3", dsn)
    if err != nil {
        return err
    }
    defer db.Close() // owner closes
    db.SetMaxOpenConns(10)
    return useDB(db)
}
```

## False Positive Notes

- Long-lived application singletons where `db.Close()` is intentionally called
  only during graceful shutdown are flagged; suppress by routing the handle
  through a lifecycle manager and adding a comment.
- Test helpers that open an in-memory DB and close it in a `t.Cleanup` may be
  flagged if the cleanup is registered indirectly; prefer an explicit
  `defer db.Close()` for clarity.
