# Code Review Rules

This document describes all rules implemented by the code review
agent. Each rule has a unique ID, category, severity, and confidence
level.

## Security Rules

### SQL_INJECTION (Critical)
- **Description**: Detects SQL queries built with string concatenation
  or `fmt.Sprintf` instead of parameterized queries.
- **Pattern**: `SELECT ... + variable`, `fmt.Sprintf("SELECT ...")`
- **Confidence**: 0.85-0.9
- **Recommendation**: Use parameterized queries with `?` or `$1`
  placeholders.

### CMD_INJECTION (Critical/High)
- **Description**: Detects shell commands built with string
  concatenation using `exec.Command("sh", "-c", ...)`.
- **Pattern**: `exec.Command("sh", "-c", cmd + userinput)`
- **Confidence**: 0.8-0.85
- **Recommendation**: Pass arguments as separate elements to
  `exec.Command`.

### HARDCODED_SECRET (Critical/High)
- **Description**: Detects hardcoded API keys, tokens, and passwords
  in source code.
- **Pattern**: `apiKey = "sk-..."`, `password := "secret123"`
- **Confidence**: 0.9-0.95
- **Recommendation**: Use environment variables or a secrets manager.

## Goroutine Leak Rules

### GOROUTINE_LEAK (High/Medium)
- **Description**: Detects goroutines started without context
  cancellation.
- **Pattern**: `go func()`, `go someFunc()` without context parameter
- **Confidence**: 0.6-0.8
- **Recommendation**: Pass `context.Context` to the goroutine and
  check `ctx.Done()`.

### CONTEXT_NOT_PASSED (Medium/Low)
- **Description**: Detects `context.Background()` or `context.TODO()`
  used in request handler code.
- **Pattern**: `context.Background()` in non-main code
- **Confidence**: 0.5-0.7
- **Recommendation**: Pass the parent context in request handlers.

## Resource Leak Rules

### UNCLOSED_RESOURCE (High)
- **Description**: Detects `os.Open`, `sql.Open`, `net.Dial` without
  matching `defer Close()`.
- **Pattern**: Resource opened but no `defer .Close()` within 5 lines
- **Confidence**: 0.85
- **Recommendation**: Add `defer resource.Close()` immediately after
  opening.

### HTTP_BODY_NOT_CLOSED (High/Medium)
- **Description**: Detects HTTP response bodies not closed with
  defer.
- **Pattern**: HTTP client call without `defer resp.Body.Close()`
- **Confidence**: 0.6-0.85
- **Recommendation**: Add `defer resp.Body.Close()` after the HTTP
  call.

## Error Handling Rules

### IGNORED_ERROR (High/Medium)
- **Description**: Detects error return values assigned to `_`.
- **Pattern**: `_ = json.Marshal(...)`, `_ = os.WriteFile(...)`
- **Confidence**: 0.55-0.85
- **Recommendation**: Handle the error or wrap it with `fmt.Errorf`.

### PANIC_IN_GOROUTINE (Critical/Medium)
- **Description**: Detects `panic()` calls, especially in goroutines.
- **Pattern**: `panic(...)` inside a `go func()` block
- **Confidence**: 0.65-0.9
- **Recommendation**: Return an error instead of using panic.

## DB Lifecycle Rules

### DB_CONNECTION_LEAK (High/Medium)
- **Description**: Detects `sql.Open` without `defer db.Close()`.
- **Pattern**: `sql.Open(...)` without `defer Close` within 10 lines
- **Confidence**: 0.7-0.85
- **Recommendation**: Add `defer db.Close()` after `sql.Open`.

### MISSING_TX_ROLLBACK (High)
- **Description**: Detects transactions started without proper
  Rollback.
- **Pattern**: `tx.Begin()` without `defer tx.Rollback()`
- **Confidence**: 0.8
- **Recommendation**: Add `defer tx.Rollback()` after Begin. The
  Commit call supersedes the deferred Rollback.

## Sensitive Info Rules

### SENSITIVE_INFO_IN_LOG (High)
- **Description**: Detects logging of sensitive information like
  passwords, tokens, or API keys.
- **Pattern**: `log.Printf("password: %s", pwd)`
- **Confidence**: 0.85
- **Recommendation**: Do not log secrets. Remove or redact sensitive
  fields before logging.

## Test Coverage Rules

### TEST_MISSING (Low)
- **Description**: Detects new exported functions without
  corresponding test files.
- **Pattern**: New `func ExportedFunc()` in a non-test .go file
- **Confidence**: 0.4 (goes to warnings)
- **Recommendation**: Create a `_test.go` file with unit tests for
  new exported functions.

## Redaction Patterns

The agent automatically redacts the following patterns in evidence:
- API keys: `api_key = "..."`, `APIKey: "..."`
- Tokens: `token = "..."`, `access_token: "..."`
- Passwords: `password = "..."`, `passwd := "..."`
- Secrets: `secret_key = "..."`
- Bearer tokens: `Bearer xxx`
- Private keys: `-----BEGIN ... PRIVATE KEY-----`
- Connection strings with passwords: `postgres://user:pass@host`
