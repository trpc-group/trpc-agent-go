# Code Review Rules

## Rules

| Rule ID | Category | Direction |
| --- | --- | --- |
| `security.secret_leak` | security | Detect API keys, tokens, passwords, JWTs, and private-key blocks in added lines. |
| `concurrency.goroutine_context_leak` | concurrency | Flag goroutines that start from added lines without a visible context/cancel path. |
| `resource.close_missing` | resource | Flag opened files, HTTP responses, or SQL rows without nearby close handling. |
| `error.ignored_error` | error | Flag `_ = err`, blank identifier errors, and ignored error-returning calls. |
| `test.missing_coverage` | test | Flag new non-test Go files when no related test file changed. |
| `db.lifecycle` | database | Flag transactions without commit/rollback and rows without close handling. |
| `security.redaction_required` | security | Record redaction when a reportable value had to be scrubbed. |

## Confidence Routing

- High-confidence critical/high items become findings.
- Medium-confidence critical/high items become `needs_human_review`.
- Low-confidence items become warnings.
- Permission denials and human-review safety decisions are governance records,
  not code findings.
