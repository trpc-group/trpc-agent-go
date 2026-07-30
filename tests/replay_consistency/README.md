# Session / Memory / Summary / Track Replay Consistency Tests

Validates that all trpc-agent-go session, memory, summary, and track backends
produce semantically equivalent results for identical sequences of operations.

## Quick Start — Lightweight Mode

No external infrastructure required. Runs InMemory vs SQLite comparison using
in-memory SQLite databases.

```bash
cd tests/replay_consistency
go test -v -run TestCrossBackendReplay
```

## Running with Extended Backends

Set environment variables to enable additional backends. Each backend is
gated by an `ENABLE` flag and configured via connection parameters.

### Redis

```bash
export REPLAY_ENABLE_REDIS=true
export REDIS_ADDR="localhost:6379"
```

### PostgreSQL

```bash
export REPLAY_ENABLE_POSTGRES=true
export PG_HOST="localhost"
export PG_PORT="5432"
export PG_USER="postgres"
export PG_PASSWORD="postgres"
export PG_DATABASE="trpc_agent_go"
```

### MySQL

```bash
export REPLAY_ENABLE_MYSQL=true
export MYSQL_HOST="localhost"
export MYSQL_PORT="3306"
export MYSQL_USER="root"
export MYSQL_PASSWORD="root"
export MYSQL_DATABASE="trpc_agent_go"
```

### ClickHouse

```bash
export REPLAY_ENABLE_CLICKHOUSE=true
export CLICKHOUSE_HOST="localhost"
export CLICKHOUSE_PORT="9000"
export CLICKHOUSE_USER="default"
export CLICKHOUSE_PASSWORD=""
export CLICKHOUSE_DATABASE="trpc_agent_go"
```

Run all enabled backends:

```bash
REPLAY_ENABLE_REDIS=true REDIS_ADDR="localhost:6379" \
REPLAY_ENABLE_POSTGRES=true PG_HOST="localhost" PG_PASSWORD="postgres" \
  go test -v -run TestCrossBackendReplay
```

Backend pairs with missing environment variables are automatically skipped.

## Replay Cases

| # | Name | Description |
|---|------|-------------|
| 1 | single_turn_text | Single user and assistant text messages |
| 2 | multi_turn_sequence | Sequential multi-turn user/assistant pairs |
| 3 | tool_call_roundtrip | Tool call with response and args extension |
| 4 | state_update_cycle | State write, overwrite, delete, clear |
| 5 | memory_write_read | Memory add, update, and search |
| 6 | summary_generation_update | Summary filter-key, version, updated time |
| 7 | summary_truncation_replay | Summary + truncated events + continuation |
| 8 | track_event_timeline | Track event ordering and payload normalization |
| 9 | interleaved_concurrency | Interleaved branch writes |
| 10 | failure_retry_dedup | Duplicate write and retry idempotency |

## Interpreting Results

The harness produces a JSON diff report at
`session_memory_summary_track_diff_report.json`.

Each diff includes:

- `case_name` — the replay scenario name
- `backend` — the backend being compared against the baseline
- `path` — dot/array-path to the differing field
- `baseline` — value from the InMemory baseline
- `actual` — value from the test backend
- `allowed_diff` — true if the difference is expected (IDs, timestamps, scores)
- `explanation` — human-readable reason

## Output Example

```json
{
  "case_name": "track_event_timeline",
  "backend": "clickhouse",
  "path": "tracks",
  "baseline": "present",
  "actual": "unsupported",
  "allowed_diff": true,
  "explanation": "track replay is unsupported on this backend"
}
```

## Backend Notes

| Backend | Session | Memory | Track |
|---------|---------|--------|-------|
| InMemory | Yes | Yes | Yes |
| SQLite | Yes | Yes | Yes |
| Redis | Yes | Yes | Yes |
| PostgreSQL | Yes | Yes | Yes |
| MySQL | Yes | Yes | Yes |
| ClickHouse | Yes | Via SQLite | **No** |

ClickHouse does not implement `session.TrackService`. Track-related diffs
for ClickHouse are marked as `allowed_diff` with the explanation "track
replay is unsupported on this backend".
