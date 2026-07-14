# Code Review Rules

| Rule ID | Category | Description |
| --- | --- | --- |
| SEC001 | security | Detect likely hard-coded API keys, tokens, passwords, and secrets. |
| GOR001 | concurrency | Detect goroutines without an obvious cancellation path. |
| CTX001 | context | Detect request code that replaces caller context with background/TODO context. |
| RES001 | resource | Detect opened files, HTTP responses, SQL rows, or DB handles without close evidence. |
| ERR001 | error_handling | Detect ignored error results. |
| DB001 | database | Detect transactions without commit/rollback evidence. |
| TEST001 | testing | Detect changed Go code without nearby test changes. |
| PANIC001 | reliability | Detect panic/log.Fatal in library-style code paths. |
