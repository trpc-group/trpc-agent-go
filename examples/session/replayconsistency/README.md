# Session Replay Consistency Harness

This harness replays the same normalized Session, Memory, Summary, and Track
operation stream through the real in-memory and SQLite Session/Memory services. It
ships ten cases covering single and multi-turn conversations, tool calls and
argument extensions, state overwrite/delete semantics, memory, summary update,
summary plus truncated events, tracks, concurrent ordering, and retry recovery.

From this directory:

```bash
# normal replay: expects no differences
go run . --output report.json

# fault campaign: injects one mismatch into every public case
go run . --inject --output report.json

go test ./...
```

The lightweight run needs no external services. Both adapters execute
`CreateSession`, `AppendEvent`, `UpdateSessionState`, `AddMemory`,
`CreateSessionSummary`, `AppendTrackEvent`, and the corresponding reads. The
persistent adapter uses temporary SQLite databases so CI remains deterministic.
Redis, Postgres, MySQL, and ClickHouse integration
runs should be enabled only when their project-specific environment variables
are configured, and unsupported pagination, TTL, Track, or query capabilities
must be added to `Snapshot.Unsupported` with a reason. Such capability gaps are
reported as documented `allowed_diff`; data loss is never allowed.

The report identifies the case, backend, session, collection locator, JSON
field path, normalized baseline and compared value, plus allowed-difference
status and explanation.
