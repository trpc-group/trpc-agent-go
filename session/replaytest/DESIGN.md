# session/replaytest design

## Goal

Issue #2001 asks for a multi-backend replay consistency harness covering
Session / Memory / Summary / Track. This package provides:

1. Deterministic public cases (`AllCases`)
2. Backend-agnostic step execution
3. Snapshot normalization
4. Diff comparison with `AllowedDiff`
5. JSON report with locator fields
6. Fault injection proving 100% detection on injected inconsistencies

## Non-goals

- No production code changes under `session/*` backends
- No real LLM calls (FakeSummarizer only)
- Root module must not force CGO; SQLite lives in `session/replaytest/sqlite`

## Pipeline

```
ReplayCase.Steps
  -> execute on each NamedBackend
  -> Snapshot
  -> Normalizer (IDs/timestamps/private keys/memory hash)
  -> Comparator (+ AllowedDiff)
  -> Report JSON
```

## Locator fields

Each error diff includes:

- `session_id`
- `event_index` (when event-related)
- `summary_filter_key` (when summary-related)
- `track_name` (when track-related)
- `memory_id` (when memory-related)
- `path`
- `baseline` / `actual`
- `allowed_diff` / `explanation`

## Cases

| Case | Focus |
|------|-------|
| single_turn_text | basic user/assistant |
| multi_turn_conversation | ordering |
| tool_call_conversation | tool call payload |
| state_crud | session state write/overwrite + delta |
| memory_write_and_read | memory service |
| summary_generation | full summary |
| summary_with_truncation | long history + post summary |
| summary_filter_key | filter-key ownership |
| track_events | track append/read |
| concurrent_interleaved | branch-local order |
| recovery_duplicate_event | duplicate writes |

## Comparison modes

- `reference` (default): compare every backend to `inmemory`
- `all_pairs`: compare every pair

## Fault injection

`InjectFault` mutates a snapshot. Tests assert every public fault kind yields
at least one non-allowed diff.

## Optional backends

Env placeholders:

- `REPLAYTEST_REDIS_ADDR`
- `REPLAYTEST_POSTGRES_DSN`
- `REPLAYTEST_MYSQL_DSN`
- `REPLAYTEST_CLICKHOUSE_DSN`

Adapters can register through `NamedBackend` without changing this package.

## Event comparison modes

- Default (`EventCompareGlobalOrdered` / empty): compare events by global index order.
- `EventCompareBranchLocal`: used by `concurrent_interleaved`. Global interleaving is relaxed; comparison still aligns events by logical ID and checks full semantics (content, tool calls, state delta, timestamps, extensions), plus branch-local order and the global key set.
