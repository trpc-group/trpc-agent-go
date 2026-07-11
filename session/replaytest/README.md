# Session / Memory replay consistency harness

This module replays the same normalized operation stream against multiple
backends and compares their observable snapshots. The lightweight suite uses
`session/inmemory` as the baseline and `session/sqlite` as the persistent
candidate, plus the matching in-memory and SQLite memory services.

## Design notes

**Normalization strategy.** Auto-generated IDs, event timestamps, JSON field
order, and map iteration order are all flattened: memory entries derive a
`semantic-*` id from a sha256 of their content and metadata, so backend-assigned
auto-increment ids are erased; timestamps are reduced to presence/boundary
booleans; JSON is canonically encoded, topics and participants are sorted, and
track payloads are compared recursively; vector similarity and retrieval scores
are intentionally excluded from comparison.

**Summary comparison strategy.** Text, filter-key, version, whether timestamps
are set, and the truncation `LastEventID` are compared field-by-field under a
strict equality contract, paired by filter key. This detects, with 100%
coverage, missing summaries, overwrite errors (stale version or wrong text),
wrong session ownership (a candidate gaining or losing a whole summary), and
the Go-specific filter-key error.

**Track comparison strategy.** Each payload is compared as a JSON tree, so a
diff can be located to a track name, an intra-track event index, and a concrete
field. `duration_ms` and other latency metrics only drift across backend run
environments, so they are classified as `allowed_diff` and never block.

**allowed_diff rules.** Declared by `DefaultAllowedRules` and matched on domain
plus a field-path suffix; a match is filed separately as `allowed_diff` and does
not affect `Passed`, while any other field mismatch blocks. A backend that lacks
paging, TTL, tracks, or a memory query must emit an `unsupported` record with
`allowed_diff: true` and a reason — it must never silently omit the capability.

**Backend onboarding.** The lightweight mode defaults to InMemory ↔ SQLite and
needs no external dependency; Redis, Postgres, MySQL, and ClickHouse are
enabled through environment variables and skipped when unset.

## Run lightweight mode

```powershell
cd session/replaytest
$env:CGO_ENABLED = "1"
go test ./... -count=1
```

The suite contains ten public cases: single turn, multi-turn ordering, state
overwrite/delete/clear, tool calls and responses, memory read/search, summary
creation and replacement, filter-key summaries, summary truncation, tracks,
concurrent append, and recovery after a failed operation. No service or API
key is required; it normally completes in well under 30 seconds.

## Reports and normalization

`compare.CompareSession` and `compare.CompareMemory` return field-level
reports. `compare.WriteReport(path, reports)` writes a JSON artifact; an
example is [`session_memory_summary_track_diff_report.json`](session_memory_summary_track_diff_report.json).
Every diff includes the case, backends, field path, baseline and candidate
values, plus a locator for session/event, state key, summary filter key, track,
or semantic memory id.

Automatic IDs are replaced with a hash of normalized memory content and
metadata. Timestamps are reduced to presence/boundary semantics, JSON objects
are canonically encoded, maps and topics are sorted, and vector scores are not
compared. Track payload is recursively compared as JSON; `duration_ms` is an
allowed difference because it measures backend execution. Summary text,
filter-key, version, cutoff and final event ID are strict comparisons. This
detects missing summaries, incorrect replacement, wrong session snapshots and
filter-key leakage without treating implementation-specific clock values as
failures.

## Optional integration backends

Redis, Postgres, MySQL and ClickHouse are intentionally not part of the
lightweight suite. Adapter tests should be enabled only when their respective
service and driver are available, using the following environment variables:

```text
REPLAY_REDIS_URL=redis://127.0.0.1:6379
REPLAY_POSTGRES_DSN=postgres://user:pass@127.0.0.1/db
REPLAY_MYSQL_DSN=user:pass@tcp(127.0.0.1:3306)/db
REPLAY_CLICKHOUSE_DSN=clickhouse://user:pass@127.0.0.1:9000/db
```

An adapter that lacks paging, TTL, tracks or a memory query must add an
`unsupported` report record with `allowed_diff: true` and a reason; it must not
silently omit the capability.
