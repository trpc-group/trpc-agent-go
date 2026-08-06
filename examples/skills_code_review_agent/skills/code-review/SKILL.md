---
name: code-review
description: Automated Go code review workflow focusing on concurrency safety, resource lifecycles, error handling, security, and DB transactions.
---

# Go Code Review Skill

This skill provides automated checks and guidelines for reviewing Go code changes.

## Review Categories

### 1. Concurrency & Context Safety
- **Goroutine Leak**: Every spawned goroutine must have a clear exit mechanism (done channel, context cancellation, or waitgroup).
- **Context Propagation**: Functions accepting `context.Context` must pass it to downstream calls and check `ctx.Done()`.

### 2. Resource Lifecycle Management
- **Resource Closure**: File handles, network connections, HTTP response bodies (`resp.Body.Close()`), and DB transactions must be closed using `defer` or explicit cleanup.

### 3. Error Handling
- **Ignored Errors**: Do not ignore returned errors (e.g. `_ = fn()`). Handle or wrap them appropriately.

### 4. Security & Credential Protection
- **Secret Exposure**: Hardcoded API keys, tokens, passwords, or private keys must be flagged and redacted.
- **Dangerous Commands**: Operations attempting system deletion (`rm -rf`) or unauthorized network exfiltration must be blocked.

### 5. Missing Test Coverage
- **Unit Tests**: New exported functions or complex internal logic must have corresponding unit tests in `*_test.go`.

### 6. Database Transaction & Connection Lifecycle
- **DB Rollback**: Transactions must `defer tx.Rollback()` before calling `tx.Commit()`.
