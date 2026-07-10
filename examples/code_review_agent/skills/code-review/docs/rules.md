# Code Review Rule Index

The following table lists all detection rules shipped with the `code-review`
skill. Each rule has a dedicated document under `docs/` describing its
evidence example, recommendation, confidence and false-positive notes.

| Rule ID | Severity | Category | Description |
| --- | --- | --- | --- |
| [SI-001](SI-001-hardcoded-secret.md) | High | Security | Detects hardcoded secrets, API keys and tokens in source. |
| [GL-001](GL-001-goroutine-no-sync.md) | High | Correctness | Flags goroutines launched without a synchronization or cancellation path. |
| [GL-002](GL-002-context-leak.md) | Medium | Correctness | Detects contexts created with `context.WithCancel`/`WithTimeout` whose cancel func is not deferred. |
| [RL-001](RL-001-resource-not-closed.md) | High | Reliability | Flags `os.Open`/`os.Create`/`http.Get` bodies not closed via `defer`. |
| [EH-001](EH-001-error-unchecked.md) | Medium | Correctness | Detects function calls whose returned `error` is discarded. |
| [TM-001](TM-001-missing-tests.md) | Low | Quality | Flags new non-test source files added without a corresponding `_test.go`. |
| [DB-001](DB-001-db-lifecycle.md) | High | Reliability | Flags `sql.Open`/`sql.DB` handles not closed or not wrapped in a lifecycle manager. |
| [SC-001](SC-001-sensitive-info.md) | High | Security | Detects sensitive identifiers (PII/credentials) leaked in logs; only scans AddedLines. |

## Severity Levels

- **High**: Likely exploitable security defect or correctness/data-loss risk.
  Block merge by default.
- **Medium**: Probable defect that should be fixed before merge but is not
  immediately exploitable.
- **Low**: Quality / maintainability issue; surface as advisory.

## Category Definitions

- **Security**: Confidentiality / integrity of secrets and user data.
- **Correctness**: Concurrency, error handling and logic defects.
- **Reliability**: Resource lifecycle, leaks and crash resilience.
- **Quality**: Test coverage and maintainability hygiene.
