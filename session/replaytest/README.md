# Session Replay Consistency

`replaytest` drives the same session, memory, summary, and track operations
through multiple backends, normalizes their snapshots, and writes localized
JSON differences.

## Lightweight Mode

The default suite compares InMemory with file-backed SQLite and needs no
external service. The repository's Session and Memory SQLite modules use
`github.com/mattn/go-sqlite3`, so this mode requires `CGO_ENABLED=1` and a C
compiler:

```bash
cd test
CGO_ENABLED=1 go test ./replayconsistency
go run ./replayconsistency/cmd/replayconsistency \
  -output /tmp/session_memory_summary_track_diff_report.json
```

The command exits non-zero when any disallowed difference exists. The ten
standard cases cover single-turn and multi-turn conversations, tool calls,
state lifecycle, memory lifecycle, summary updates, event truncation, tracks,
concurrent appends, and retry recovery.

## Integration Mode

Set any combination of these variables:

```bash
export REPLAY_REDIS_URL='redis://localhost:6379/0'
export REPLAY_POSTGRES_DSN='postgres://user:pass@localhost:5432/replay?sslmode=disable'
export REPLAY_MYSQL_DSN='user:pass@tcp(localhost:3306)/replay?parseTime=true'
export REPLAY_CLICKHOUSE_DSN='clickhouse://user:pass@localhost:9000/replay?output_format_native_write_json_as_string=1'

cd test
go run ./replayconsistency/cmd/replayconsistency \
  -integration -timeout 5m \
  -output /tmp/session_memory_summary_track_diff_report.json
```

Unset integrations are included in the report as `unsupported` with
`allowed_diff: true`. ClickHouse currently exercises Session Events, State,
and TTL; Memory, Summary, and Track are explicitly reported as unsupported.
The DSN setting shown above is required by ClickHouse 25.x to decode native
JSON columns as strings. Redis, Postgres, and MySQL exercise both Session and
Memory. Each case uses an isolated application/user/session key, and the runner
deletes replay data before and after execution.

## Design

The harness separates backend-neutral replay scripts from concrete service
construction. A runner executes typed operations through the public
`session.Service`, optional `session.TrackService`, and `memory.Service`
contracts, then captures events, state transitions, memory reads and searches,
filter-key summaries, reconstructed compressed context, tracks, and retry
observations. Normalization replaces generated UUIDs with stable aliases,
canonicalizes JSON values and tags, sorts unordered metadata, truncates time to
millisecond precision, derives semantic memory IDs, rounds similarity scores,
and extracts portable duration/error fields from track payloads. Summary
comparison retains filter key, session ownership, boundary version, update
time, revision, replacement relation, and last covered event. Concurrent cases
preserve raw commit order for diagnostics while comparing events by an explicit
logical sequence; raw-order differences are visible but allowed. Capability
gaps are never silently skipped: each becomes an `unsupported` record with an
explanation. Additional allowed-diff rules require a field-path prefix,
optional backend, reason, and optional numeric tolerance. All other mismatches
fail the run. Reports localize differences by session ID plus event index,
memory ID, summary ID/filter key, or track name, and distinguish backend
mismatches from expectation violations. The lightweight factories use InMemory
and SQLite, while environment-selected factories add Redis, Postgres, MySQL,
and ClickHouse without changing replay cases.

## Report

The schema version is `trpc-agent-go/replay-diff/v1`. See
[`test/replayconsistency/testdata/session_memory_summary_track_diff_report.json`](../../test/replayconsistency/testdata/session_memory_summary_track_diff_report.json)
for localized allowed and disallowed examples.
