# SQLite replay adapter

This module binds `session/replaytest` to the file-backed Session and Memory
SQLite services. Each case uses two temporary database files and synchronous
session persistence. The adapter configures the same deterministic summarizer
and filter-key policy as the InMemory reference.

SQLite requires CGO and a C compiler:

```bash
CGO_ENABLED=1 go test ./... -run TestLightweightReplayMatrix -count=1
```

The integration test runs all public cases and enforces the issue's 30-second
lightweight-mode limit.
