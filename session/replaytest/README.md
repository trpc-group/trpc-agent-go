# Session / Memory Multi-Backend Replay Consistency Framework

## Design Overview (150–300 words)

The replay consistency framework drives identical operation sequences through
multiple session, memory, summary, and track backends, then compares the
observable state using normalized representations. A **ReplayCase** defines an
ordered list of **ReplayOperation** steps (append event, update state, add
memory, create summary, append track event, etc.). The **Harness** executes each
case on every registered **BackendFactory**, captures a **BackendSnapshot**, and
passes pairs of snapshots to a **Comparator** that emits **DiffEntry** records.

**Normalization strategy.** Auto-generated IDs, precise timestamps, and JSON key
ordering are stripped before comparison. Events are reduced to author, role,
content, branch, tag, filter-key, state-delta keys/values, extension keys, and
choice-level tool call names. Summaries are compared by text, topics, filter-key,
boundary version, and boundary filter-key; cutoff timestamps and last-event IDs
are marked `allowed_diff` since they naturally drift across storage engines.
Track payloads are normalized via deterministic JSON re-marshalling. Memory
entries are sorted by content and compared by content, topics, scope, kind, and
location—IDs are not compared directly.

**Summary comparison** verifies presence per filter-key, text equality, topic
sets, boundary version, and boundary filter-key. Summary loss, overwrites, and
session-ownership errors are detected at 100% rate. Filter-key mismatches
produce explicit non-allowed diffs.

**Track comparison** matches tracks by name, then compares event payloads
(normalized JSON) per index. Missing tracks or count mismatches are flagged.

**Allowed-diff rules** tolerate: timestamp drift ≤ 2 s, float drift ≤ 1%,
backend-generated IDs, JSON key reordering, and extension key order.

**Backend integration** uses factory functions: register `session.Service`,
`session.TrackService`, and `memory.Service` creators via `BackendFactory`.

## Package Layout

```
session/replaytest/
├── types.go              # Core types (OpType, ReplayCase, BackendSnapshot, DiffEntry, etc.)
├── normalizer.go         # Normalization functions for events, state, memories, summaries, tracks
├── comparator.go         # Comparator producing DiffEntry list
├── report.go             # JSON report builder
├── backend.go            # Backend factory helpers (InMemory, env-gated optional)
├── harness.go            # Harness execution engine
├── cases.go              # 12 replay case definitions
├── replay_test.go        # Unit tests + InMemory baseline test + report generator
└── sqliteintegration/    # Separate Go module for SQLite integration tests
    ├── go.mod
    └── integration_test.go
```

## Running

### Light mode (InMemory only, no CGO required)

```bash
go test ./session/replaytest/... -v -timeout=30s
```

This runs 14 tests covering normalizer, comparator, report builder, the harness
with two independent InMemory backends, and generates
`session_memory_summary_track_diff_report.json`.

### With SQLite (requires CGO + gcc)

```bash
CGO_ENABLED=1 go test ./session/replaytest/sqliteintegration/... -v -timeout=30s
```

### With Redis / Postgres / MySQL / ClickHouse (optional)

Set the corresponding environment variable to enable:

| Backend     | Environment Variable       |
|-------------|---------------------------|
| Redis       | `REPLAY_REDIS_ADDR`        |
| PostgreSQL  | `REPLAY_POSTGRES_DSN`      |
| MySQL       | `REPLAY_MYSQL_DSN`         |
| ClickHouse  | `REPLAY_CLICKHOUSE_DSN`    |

For these backends, construct a `BackendFactory` and pass it via
`WithBackends()`:

```go
redisFactory := replaytest.BackendFactory{
    Name: "redis",
    CreateSession: func() (session.Service, error) {
        return redisSess.NewService(redisSess.WithAddr(os.Getenv("REPLAY_REDIS_ADDR")))
    },
    CreateMemory: func() (memory.Service, error) {
        return redisMem.NewService(redisMem.WithAddr(os.Getenv("REPLAY_REDIS_ADDR")))
    },
}
harness := replaytest.NewHarness(replaytest.WithBackends(redisFactory))
```

### Registering the SQLite backend

```go
sqliteFactory := replaytest.BackendFactory{
    Name: "sqlite",
    CreateSession: func() (session.Service, error) {
        db, _ := sql.Open("sqlite3", ":memory:")
        return sessSQLite.NewService(db)
    },
    CreateMemory: func() (memory.Service, error) {
        db, _ := sql.Open("sqlite3", ":memory:")
        return memSQLite.NewService(db)
    },
}
harness := replaytest.NewHarness(replaytest.WithBackends(sqliteFactory))
```

## Replay Cases (12 cases)

| # | Name                          | Coverage                                            |
|---|-------------------------------|-----------------------------------------------------|
| 1 | single_turn_conversation      | One user + one assistant message                    |
| 2 | multi_turn_conversation       | Three rounds of user/assistant exchange             |
| 3 | tool_call_conversation        | Tool call with args + tool response + follow-up     |
| 4 | state_updates                 | Write, overwrite, delete session state keys         |
| 5 | memory_write_read             | Add memory entries and verify readability           |
| 6 | summary_generation            | Append events then trigger session summary          |
| 7 | summary_event_truncation      | Long conversation → summary → new events appended   |
| 8 | track_events                  | Tool execution timing and error track events        |
| 9 | concurrent_writes             | Interleaved tool call and response events           |
|10 | error_recovery                | Simulated write failure then retry                  |
|11 | state_delete_clear            | Delete specific key, then clear remaining state     |
|12 | multiple_memory_entries       | Add several memories, clear all, verify empty       |

## Report Format

The JSON report (`ReplayReport`) contains:

```json
{
  "timestamp": "2026-07-11T14:37:39Z",
  "total_cases": 12,
  "pass_cases": 12,
  "fail_cases": 0,
  "total_diffs": 0,
  "allowed_diffs": 0,
  "case_results": [
    {
      "case_name": "single_turn_conversation",
      "backend_pairs": [["inmemory-A", "inmemory-B"]],
      "has_diff": false,
      "diff_count": 0,
      "allowed_diff_count": 0
    }
  ],
  "backend_names": ["inmemory-A", "inmemory-B"]
}
```

Each `DiffEntry` in `differences` carries: `session_id`, `event_index`,
`summary_filter_key`, `memory_id`, `track_name`, `field_path`, `base_backend`,
`base_value`, `compare_backend`, `compare_value`, `allowed_diff`, `explanation`.
