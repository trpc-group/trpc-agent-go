# Code Review Rules

Rule categories enforced by the CR Agent (Phase 1 deterministic + Phase 2 sandbox):

| Category | Rule IDs | Description |
|----------|----------|-------------|
| Security | SEC-001, SEC-002 | SQL/command injection via string concatenation |
| Concurrency | CONC-001, CONC-002 | Goroutine leaks, missing context propagation |
| Resource | RES-001, DB-001 | Unclosed files/connections, missing tx commit/rollback |
| Error handling | ERR-001 | Ignored errors, empty error branches |
| Sensitive data | SENS-001 | Hardcoded credentials, secrets in logs |
| Testing | TEST-001 | Exported functions without test file changes |

Sandbox scripts complement regex rules with diff validation and optional `go vet` / `go test`.
