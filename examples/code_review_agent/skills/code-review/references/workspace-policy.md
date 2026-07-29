# Workspace policy

The review workspace is isolated, starts with a clean environment, and has no
network capability. Go commands run with module proxy, checksum database, and
automatic toolchain download disabled.

Only these complete commands are authorized:

- `go test ./...`
- `go vet ./...`
- `staticcheck ./...`
- immutable trusted Skill scripts staged by the host under `.review-trusted/`

Do not request dependency installation. Approval prompts are not simulated and
will not execute a command.

