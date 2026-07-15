# Review Fixtures

These diff fixtures exercise the first-version deterministic review path.

- `safe.diff`: clean Go change
- `secret.diff`: potential secret leakage
- `secret-shapes.diff`: common API key, LLM key, bearer, GitHub token, and placeholder cases
- `panic.diff`: direct panic path
- `todo.diff`: TODO marker
- `test-missing.diff`: missing-test warning
- `goroutine.diff`: goroutine-oriented sample for future rules
- `context.diff`: context-oriented sample for future rules
- `resource.diff`: resource lifecycle sample for future rules
- `db-lifecycle.diff`: database lifecycle sample for future rules
- `http-body.diff`: HTTP response body close rule
- `sql-string-concat.diff`: SQL string concatenation security rule
- `command-injection.diff`: shell/dynamic command execution security rule
- `context-background.diff`: context propagation misuse rule
- `mutex-unlock.diff`: mutex lock without visible unlock rule
- `defer-in-loop.diff`: defer inside loop rule
- `bare-return-err.diff`: unwrapped bare error return rule
- `string-concat-loop.diff`: low-confidence string concatenation in loop warning
- `realistic-service-risk.diff`: multi-file PR-shaped sample that combines secret, panic, goroutine, context, resource, database, TODO, and missing-test risks
