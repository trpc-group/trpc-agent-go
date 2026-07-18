# Go review rules

| Rule | Category | Review intent |
|---|---|---|
| `go/security/hardcoded-secret` | Security | Reject credential literals and redact their values everywhere. |
| `go/security/dynamic-shell` | Security | Flag shell interpretation or dynamically assembled commands. |
| `go/context/cancel-leak` | Context | Require ownership of `CancelFunc` returned by context constructors. |
| `go/concurrency/unbounded-goroutine` | Concurrency | Require cancellation and a visible completion mechanism. |
| `go/resource/close` | Resource lifecycle | Close files, HTTP bodies, and SQL rows after successful acquisition. |
| `go/database/transaction-rollback` | Database lifecycle | Install a rollback guard before transaction work. |
| `go/database/sql-concatenation` | Database security | Bind values rather than constructing SQL text. |
| `go/error/ignored` | Error handling | Handle or propagate returned errors. |
| `go/error/swallowed` | Error handling | Do not convert a failure path into success. |
| `go/test/missing-change` | Test coverage | Send production changes without test changes to human review. |

High-confidence findings are actionable. A lower-confidence heuristic or missing-test observation belongs in warnings or human review. Sandbox failures are separate from code findings and must remain visible in governance and monitoring sections.
