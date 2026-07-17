# Go Review Rules

- Security and secrets: flag hard-coded API keys, tokens, passwords, bearer
  tokens, or secret-like constants in changed lines.
- Goroutine and context lifecycle: flag new goroutines that do not visibly pass
  context or select on `ctx.Done()`.
- Resource lifecycle: flag opened files, HTTP responses, SQL rows, and database
  handles when no close is visible nearby.
- Error handling: flag discarded errors such as `, _ :=` or `, _ =`.
- Database lifecycle: flag new `sql.Open` calls when ownership and close
  behavior are unclear.
- Tests: implementation-only Go changes should include a focused test change or
  an explicit reason why existing coverage is enough.
