# Code Review Rules

## Overview

This document describes all built-in code review rules provided by the
code-review skill. Each rule checks for a specific category of code issues
and produces structured `Finding` results.

Rules are organized into 6 categories covering 10 individual checks.

## Rule Categories

### 1. Security (`security`)

| Rule ID | Severity | Description |
|---------|----------|-------------|
| `GO_SECURITY_INJECTION` | critical | Detects SQL injection via string concatenation, command injection via `exec.Command` with shell flags, and hardcoded credentials |
| `GO_SECURITY_HARDCODED_KEY` | critical | Detects inline API keys (sk-...), GitHub tokens (ghp_/ghs_...), and private key blocks |

**Examples**: String concatenation in SQL queries, `exec.Command("sh", "-c", ...)` with user input, `apiKey = "sk-..."` in source.

### 2. Goroutine & Context Leak (`goroutine_leak`)

| Rule ID | Severity | Description |
|---------|----------|-------------|
| `GO_GOROUTINE_NO_CANCEL` | high | Detects goroutines launched via `go func()` without `ctx.Done()` handling, and `context.WithCancel`/`WithTimeout` without `defer cancel()` |

**Examples**: `go func() { for { process() } }()` missing select on ctx.Done; `ctx, cancel := context.WithCancel(ctx)` without `defer cancel()`.

### 3. Resource Leak (`resource_leak`)

| Rule ID | Severity | Description |
|---------|----------|-------------|
| `GO_RESOURCE_NO_CLOSE` | high | Detects `os.Open`, `os.Create`, `os.OpenFile`, `http.Get`, `db.Query` calls without a corresponding `defer .Close()` |

**Examples**: `f, _ := os.Open(path)` without `defer f.Close()`; `resp, _ := http.Get(url)` without `defer resp.Body.Close()`.

### 4. Error Handling (`error_handling`)

| Rule ID | Severity | Description |
|---------|----------|-------------|
| `GO_ERROR_SILENT_IGNORE` | medium | Detects `_ = fn()` patterns that silently discard error return values |
| `GO_ERROR_NO_RETURN` | medium | Detects `if err := ...; err != nil` blocks that do not return or log the error |

**Examples**: `_ = doSomething()`; `if err := validate(); err != nil { _ = err }`.

### 5. Test Coverage (`missing_test`)

| Rule ID | Severity | Description |
|---------|----------|-------------|
| `GO_TEST_MISSING_FUNC` | medium | Detects exported functions (starting with uppercase) without a corresponding `TestXxx` function |
| `GO_TEST_FILE_MISSING` | medium | Detects source files with exported symbols but no corresponding `_test.go` file |

### 6. Database Lifecycle (`db_lifecycle`)

| Rule ID | Severity | Description |
|---------|----------|-------------|
| `GO_DB_TX_NO_ROLLBACK` | high | Detects `db.Begin()` / `db.BeginTx()` without `defer tx.Rollback()`, missing `rows.Err()` check, and HTTP calls inside transactions |
| `GO_DB_ROWS_NO_ERRCHECK` | medium | Detects `for rows.Next()` loops without a subsequent `rows.Err()` check |

## Running Rules

Rules are automatically loaded by the `RuleRegistry` and executed against
each changed file in the diff. Filters control which rules are enabled.

To disable a specific rule:

```yaml
disabled_rules:
  - GO_TEST_FILE_MISSING
```

To override severity:

```yaml
severity_overrides:
  GO_ERROR_SILENT_IGNORE: "low"
```
