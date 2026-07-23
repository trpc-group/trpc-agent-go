# Code Review Rules

The deterministic rule engine emits structured findings with `severity`,
`category`, `file`, `line`, `title`, `evidence`, `recommendation`,
`confidence`, `source`, and `rule_id`.

Rule IDs:

- `secret.hardcoded`: high severity security finding for hardcoded tokens,
  API keys, passwords, private keys, bearer credentials, and common provider
  credential formats.
- `security.shell_command_injection`: high severity finding for shell command
  strings executed through `sh -c` or `bash -c`.
- `security.insecure_tls`: high severity finding when TLS certificate
  verification is disabled.
- `concurrency.goroutine_context_leak`: medium severity finding for goroutines
  not tied to cancellation context.
- `resource.unclosed_file`: medium severity finding for opened files without a
  close path.
- `resource.unclosed_http_body`: medium severity finding for HTTP responses
  without `Body.Close`.
- `resource.unclosed_sql_rows`: medium severity finding for SQL rows without
  `Close`.
- `error.ignored_return`: medium severity finding for explicitly ignored error
  returns.
- `database.tx_lifecycle`: high severity finding for transactions without
  commit or rollback.
- `database.sql_open_lifecycle`: medium severity finding for database handles
  without a close or documented owner.
- `tests.missing_tests`: low severity warning for changed exported Go behavior
  without matching test updates.
- `governance.command_blocked`: low confidence warning for command-gate deny.
- `governance.permission_error`: low confidence warning for permission deny,
  ask, or policy errors.

Confidence is deterministic. Values at or above `0.80` become findings; lower
values become warnings and require human review.
