# Issue 2002 demo

Runs the Guard against the sample matrix from the issue (safe go test,
destructive delete, credential path, network allow/deny, shell wrapper,
pipeline, install ask, long sleep, oversized stdin, secret, hostexec ask,
code_blocks).

```bash
go run .
```

No model API key needed: the demo calls `CheckToolPermission` directly.
