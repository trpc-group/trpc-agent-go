# replaytest/sqlite

Optional SQLite backend factory for `session/replaytest`.

This is a separate Go module so the root `trpc-agent-go` consumers are not forced
to compile `github.com/mattn/go-sqlite3` (CGO).

## Test

```bash
cd session/replaytest/sqlite
go test .
```

Requires a CGO-capable toolchain and a working C compiler for `mattn/go-sqlite3`.
