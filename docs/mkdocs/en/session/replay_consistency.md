# Cross-Backend Replay Consistency

`session/replaytest` runs the same trajectory against multiple Session and Memory backends, captures their public replay-visible state, and reports semantic differences. The core matrix in `test/replay_consistency_test.go` compares InMemory with SQLite. `test/replay_consistency_external_test.go` also provides an always-on miniredis simulator test and environment-gated real-service tests.

## Run the lightweight matrix

Run the comparator unit tests from the repository root:

```bash
go test ./session/replaytest -count=1
```

The full local suite needs no external service and covers InMemory, SQLite, and miniredis. SQLite requires CGO; unconfigured real backends are skipped independently:

```bash
cd test
CGO_ENABLED=1 go test . -run ReplayConsistency -count=1
```

Set `TRPC_AGENT_REPLAY_REPORT_PATH` to write the healthy matrix report:

```bash
cd test
CGO_ENABLED=1 TRPC_AGENT_REPLAY_REPORT_PATH="$PWD/replay-report.json" \
  go test . -run '^TestReplayConsistencyMatrix$' -count=1
```

The injected-difference example is `session/replaytest/testdata/session_memory_summary_track_diff_report.json`. A diff includes the case and backend names, session ID, exact field path, both values, and location context such as event index, stable memory ID, summary filter key, or track name.

## Real replay cases

The public matrix runs these 12 cases against real InMemory and SQLite services:

1. `single_turn`: one user event followed by one assistant event.
2. `multi_turn`: three ordered user/assistant turns.
3. `tool_call_and_response`: tool call, JSON arguments, tool response, and tool-call-args extension.
4. `state_update_overwrite_delete`: app and user state deletion, plus session state updates, overwrites, and explicit null values.
5. `session_state_direct_round_trip`: creates, overwrites, and reads Session State without depending on Events.
6. `memory_search_order_and_score`: searches fact and episodic memories and compares rank plus normalized score.
7. `memory_update_and_delete`: persistent update and deletion semantics.
8. `summary_filter_and_update`: filter-key summary generation and replacement.
9. `summary_event_window_recovery`: a summary plus the two retained tail events loaded through `WithEventNum(2)`.
10. `track_status_and_error`: ordered started and failed Track payloads.
11. `concurrent_tool_event_interleaving`: six goroutines write tool events concurrently, then deterministic order is restored from predefined logical timestamps.
12. `failure_recovery_without_duplicates`: a real retry follows the first failed attempt; an ambiguous successful write then loses its acknowledgement and is read before any further retry.

Every public case injects a corresponding backend drift after SQLite load but before normalization, exercising Load, Capture, Normalize, and Compare end to end. Each must produce a located, non-allowed diff. Separate tests cover 21 fine-grained event, state, memory, summary, and Track mutations plus four raw Summary faults.

## Normalization and reports

Events are normalized after cloning the Session. Event, invocation, and tool-call IDs become stable aliases; generated timestamps, request IDs, and response creation times are removed. StateDelta values, tool arguments and responses, extensions, and Track payloads are decoded as JSON with number precision preserved.

Memory keeps backend read order by default, including `rank`, `score` quantized to six decimal places, application/user scope, content, and metadata. Stable memory IDs are assigned after normalization. A case may set `UnorderedMemories` only when its contract explicitly treats memory order as irrelevant; that case receives semantic sorting and rank `-1`. The concurrent event case explicitly sets `OrderEventsByTimestamp` to restore logical order.

Summary snapshots report application, user, and session ownership; map and boundary filter keys; boundary presence and version; text and topics; and normalized event indexes for update time, cutoff time, and `LastEventID`. Track snapshots retain the track name, event order, and normalized payload. Only payload keys configured as volatile, such as duration or latency, are removed.

`CaseReport.capabilities` records every backend's capability map. An unsupported built-in section is omitted from capture and semantic comparison, but it must have a reason. Capability health is scoped to the current case: an unrelated unsupported capability does not fail a case that neither requires nor skips it. A skipped required capability must explicitly set `allowed_diff: true`; otherwise the report is unhealthy.

Each public Case declares the capabilities required for execution through `RequiredCapabilities`. The Harness requires every backend to declare those capabilities explicitly. A missing declaration is an error instead of silently inheriting the default-supported behavior. The baseline must support every requirement; a comparison backend with an explicitly unsupported requirement is not executed and records the capability name in `skipped_backends`. If no candidate backend completes a comparison, the report sets `inconclusive: true`, and `HasUnexpectedDiff` does not treat it as healthy. Fine-grained semantics that must still run and produce a diff, such as `event_state_delta_null`, are not hard prerequisites.

A sub-capability can describe partial semantics without suppressing its whole section. A nil value in `Event.StateDelta` is an explicit JSON null (a replay tombstone), not physical key deletion; `Session.DeleteState` performs deletion. Real Redis HashIdx preserves that null, while miniredis's Lua/cjson emulation keeps the previous value. Only the `miniredis` fixture therefore advertises `event_state_delta_null` as unsupported/allowed. State is still compared in full, with exact allowances only for `$.state.remove_me` and `$.state.pending`; tests require both differences to occur so the allowance cannot become stale or broad.

A missing path and an explicit JSON `null` are different. Reports use `baseline_present` and `compared_present`; an absent side contains `{"missing": true}`, while a present null remains `null`.

An `AllowedDiff` must name both backends, one section, one exact path, and a reason. Wildcards are rejected, and the path must belong to the declared section. For example:

```go
replaytest.AllowedDiff{
	Section:  "tracks",
	Path:     "$.tracks.tool[0].payload.backend_note",
	BackendA: "inmemory",
	BackendB: "sqlite",
	Reason:   "SQLite exposes a backend-only diagnostic note",
}
```

## Optional external backends

`TestReplayConsistencyExternalBackends` wires Redis, PostgreSQL, MySQL, and ClickHouse. Each subtest reads only its own variable and skips when unset. When configured, it runs the same 12-case catalog: cases whose requirements are supported compare with a separate InMemory baseline, while the rest are explicitly inconclusive/skipped:

| Backend | Enable when set | Example | Service wiring |
| --- | --- | --- | --- |
| Redis | `TRPC_AGENT_REPLAY_REDIS_URL` | `redis://localhost:6379/15` | `session/redis.WithRedisClientURL` and `memory/redis.WithRedisClientURL` |
| PostgreSQL | `TRPC_AGENT_REPLAY_POSTGRES_DSN` | `postgres://user:pass@localhost:5432/replay?sslmode=disable` | `session/postgres.WithPostgresClientDSN` and `memory/postgres.WithPostgresClientDSN` |
| MySQL | `TRPC_AGENT_REPLAY_MYSQL_DSN` | `user:pass@tcp(localhost:3306)/replay?parseTime=true` | `session/mysql.WithMySQLClientDSN` and `memory/mysql.WithMySQLClientDSN` |
| ClickHouse | `TRPC_AGENT_REPLAY_CLICKHOUSE_DSN` | `clickhouse://user:pass@localhost:9000/replay` | `session/clickhouse.WithClickHouseDSN`; the reported candidate name is `clickhouse-session+inmemory-memory`, while Events, Summary, and Track are unsupported/allowed |

For example, run only the real Redis integration:

```bash
cd test
CGO_ENABLED=1 TRPC_AGENT_REPLAY_REDIS_URL='redis://localhost:6379/15' \
  go test . -run '^TestReplayConsistencyExternalBackends/redis$' -count=1 -v
```

PostgreSQL, MySQL, and ClickHouse use the corresponding table variable and test-name suffix. Constructors use the same deterministic summarizer and Summary filter-key policy plus a fixed table prefix, and every case receives a unique AppName. PostgreSQL and MySQL disable soft deletion and physically clean Session, Memory, App State, and User State afterward. PostgreSQL currently stores timestamps without a zone, so run Summary integration with `TZ=UTC`; otherwise local session times and UTC Summary boundaries can be read one zone apart. ClickHouse isolates tombstoned data with unique namespaces. Its 25.3 JSON schema cannot losslessly round-trip dotted extension keys, explicit null values, or the Summary String scan, so direct State and Memory cases run while the rest are explicitly skipped. Use dedicated test databases with schema-creation privileges. Test logging never prints a DSN deliberately.

## Design summary

The framework clones a Session, decodes replay data as precision-preserving JSON, and maps generated IDs to stable aliases. Memory keeps order, rank, quantized score, and scope unless a Case explicitly declares it unordered. Summary compares ownership, filter keys, boundary versions, and event indexes; Track keeps its name, order, and payload while timing fields are removed. An allowed difference requires both backends, a section, an exact path, and a reason, while missing and null remain distinct. Capabilities can skip a whole unsupported section or record partial semantics while comparison continues. External tests construct real Backends from environment variables and skip each unconfigured service independently.
