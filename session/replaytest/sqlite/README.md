# SQLite replay integration test

This source-tree test module binds `session/replaytest` to the file-backed
Session and Memory SQLite services. It intentionally exposes no importable
adapter: a separately published adapter cannot depend on the new root package
until that package has been released. Each case uses two temporary database
files and synchronous session persistence with the same deterministic
summarizer and filter-key policy as the InMemory reference.

SQLite requires CGO and a C compiler:

```bash
CGO_ENABLED=1 go test ./... -run TestLightweightReplayMatrix -count=1
```

The integration test runs all public cases and enforces the issue's 30-second
lightweight-mode limit.
