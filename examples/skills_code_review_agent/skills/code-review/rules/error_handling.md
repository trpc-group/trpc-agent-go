# EH-001 - Error Return Value Not Checked

**Severity:** medium  
**Category:** error_handling

## Description

An error value is assigned via `:=` or `=` but the next two lines in the hunk
contain no `err != nil` check or `return err`. Ignoring errors silently hides
failures and makes debugging difficult.

## Confidence

Low - the check window is limited to two lines; real code may check the error
farther down. Findings are placed in the warnings section.

## Fix

```go
result, err := someCall()
if err != nil {
    return fmt.Errorf("someCall: %w", err)
}
```

Intentionally-ignored errors must be logged rather than silently discarded:

```go
if err := db.InsertRecord(row); err != nil {
    log.Printf("insert record: %v", err)
}
```
