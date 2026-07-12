# Session Replay Consistency Harness

This harness replays the same normalized Session, Memory, Summary, and Track
snapshots through an in-memory backend and a local persistent JSON backend. It
ships ten cases covering single and multi-turn conversations, tool calls and
argument extensions, state overwrite/delete semantics, memory, summary update,
summary plus truncated events, tracks, concurrent ordering, and retry recovery.

From `examples/session`:

```bash
# normal replay: expects no differences
go run ./replayconsistency --output report.json

# fault campaign: injects one mismatch into every public case
go run ./replayconsistency --inject --output report.json

go test ./replayconsistency
```

The lightweight run needs no services. The persistent adapter intentionally
uses a local JSON file so CI remains deterministic; a SQL adapter can implement
the same `Backend` interface. Redis, Postgres, MySQL, and ClickHouse integration
runs should be enabled only when their project-specific environment variables
are configured, and unsupported pagination, TTL, Track, or query capabilities
must be added to `Snapshot.Unsupported` with a reason. Such capability gaps are
reported as documented `allowed_diff`; data loss is never allowed.

The report identifies the case, backend, session, collection locator, JSON
field path, normalized baseline and compared value, plus allowed-difference
status and explanation.
