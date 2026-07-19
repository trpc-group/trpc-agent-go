# Session / Memory Multi-Backend Replay Consistency Testing Framework

## Overview

`replaytest` is a multi-backend replay consistency testing framework for tRPC-Agent-Go's Session and Memory services. It drives multiple backends through identical sequences of operations defined as JSON scenarios, captures normalized snapshots, and produces field-level diff reports to detect cross-backend inconsistencies.

**Design rationale**: Different Session / Memory backends (InMemory, SQLite, Redis, Postgres, MySQL, ClickHouse) must behave identically when processing the same agent trajectory. Divergence in event order, state values, memory contents, or summary metadata causes replay corruption. This framework:

1. **Captures** a canonical `ReplaySnapshot` from each backend after replaying an identical sequence of operations (events, state updates, memory CRUD, summaries, tracks).
2. **Normalizes** auto-generated fields (IDs, timestamps, JSON ordering, map iteration order) to make snapshots comparable.
3. **Compares** two snapshots section-by-section (session, events, state, memory, summary, tracks) using recursive deep-diff.
4. **Reports** field-level differences with path, values, and context (event index, memory ID, summary filter-key, track name).

**Normalization strategy**: Strip `id`, `timestamp`, `created` from events and responses. Decode JSON-encoded state values. Sort memory entries by deterministic key and track events by name. Render all timestamps as UTC RFC 3339. Canonicalize JSON values via marshal→unmarshal to eliminate map/slice ordering differences.

**Allowed-diff rules**: Cross-backend differences that are expected (e.g., non-deterministic concurrent event ordering, SQLite summary serialization gaps) can be marked as `allowed_diffs` in scenario JSON files via wildcard path patterns with explanatory reasons.

## Quick Start

```bash
# Run lightweight mode (InMemory vs SQLite, ~110ms, zero external deps)
cd test && go test ./replaytest/... -v

# Run specific cases
go test ./replaytest/... -run TestReplayConsistency_AllCases -v
```

## Lightweight Mode

Compares **InMemory** against **SQLite** using temporary databases created in `t.TempDir()`. No external services required, runs in ~110ms. This is the default and always-on mode.

Acceptance target: ≤ 30 seconds for the full 10-case suite.

## Integration Mode

Additional backends can be enabled via environment variables (future enhancement):

| Variable | Backend |
|----------|---------|
| `TRPC_AGENT_REPLAY_REDIS_ADDR` | Redis |
| `TRPC_AGENT_REPLAY_POSTGRES_DSN` | PostgreSQL |
| `TRPC_AGENT_REPLAY_MYSQL_DSN` | MySQL |
| `TRPC_AGENT_REPLAY_CLICKHOUSE_DSN` | ClickHouse |
| `TRPC_AGENT_REPLAY_REPORT_PATH` | Custom diff report output path |

## Replay Case JSON Format

Each replay case is a JSON file with the following structure:

```json
{
  "name": "case_name",
  "description": "What this case tests",
  "app_name": "my-app",
  "user_id": "user-id",
  "session_id": "session-id",
  "steps": [...],
  "verify": {"events_count": 5},
  "allowed_diffs": [...]
}
```

### Step Types

| Type | Description | Relevant Field |
|------|-------------|---------------|
| `create_session` | Create a new session with optional initial state | `state` |
| `append_event` | Append an event to the session | `event` |
| `update_app_state` | Update app-level state | `state` |
| `update_user_state` | Update user-level state | `state` |
| `update_session_state` | Update session-level state | `state` |
| `add_memory` | Add a memory entry | `memory` |
| `update_memory` | Update a memory entry by alias | `memory` |
| `delete_memory` | Delete a memory entry by alias | `memory` |
| `create_summary` | Create or update a session summary | `summary` |
| `append_track` | Append a track event | `track` |
| `concurrent_events` | Execute sub-steps in parallel goroutines | `concurrent` |
| `get_session` | Snapshot point for verification | — |

### Event Spec Fields

`author`, `role`, `content`, `tool_calls` (array of `{id, name, arguments}`), `tool_id`, `tool_name`, `branch`, `filter_key`, `tag`, `state_delta`, `extensions`, `actions` (`{skip_summarization}`)

**Important**: Every session must include at least one user message (`role: "user"`), as the session event filtering requires a user message anchor.

### Memory Spec Fields

`op` ("add" / "update" / "delete"), `ref` (alias reference for update/delete), `result_alias` (store memory ID for later reference), `content`, `topics`, `metadata` (`{kind, event_time, participants, location}`).

### Summary Spec Fields

`filter_key` (which event filter key the summary covers), `text` (deterministic summary text, no LLM required), `force` (force re-generation).

### Track Spec Fields

`name` (track identifier), `payload` (arbitrary JSON object).

## Diff Report Format

The diff report is a JSON array of `DiffEntry` objects:

```json
{
  "case": "case_name",
  "session_id": "session-id",
  "backend_a": "in_memory",
  "backend_b": "sqlite",
  "section": "events",
  "path": "$.events[0].content",
  "left": "hello from backend A",
  "right": "hello from backend B",
  "allowed": false,
  "reason": "",
  "context": {
    "event_index": 0
  }
}
```

- **path**: JSONPath-like path to the differing field
- **context**: Locator information (event_index, memory_id, summary_filter_key, track_name, track_event_index)
- **allowed**: Whether this diff matches an `allowed_diffs` rule
- **reason**: Explanation from the matching allowed-diff rule

### Context Locators

| Section | Context Fields |
|---------|---------------|
| `events` | `event_index` |
| `memory` | `left_memory_key`, `left_memory_id`, `right_memory_key`, `right_memory_id` |
| `summary` | `summary_filter_key` |
| `tracks` | `track_name`, `track_event_index` |

## Feature Coverage

Both lightweight-mode backends (InMemory, SQLite) fully support every operation exercised by the replay framework. There are no cross-backend functional gaps.

| Operation | InMemory | SQLite |
|-----------|:--------:|:------:|
| Session create / event append | ✅ | ✅ |
| State update (app / user / session) | ✅ | ✅ |
| Memory CRUD | ✅ | ✅ |
| Session summary | ✅ | ✅ |
| Track event append | ✅ | ✅ |
| Concurrent event writes | ✅ | ✅ |

If integrated backends (Redis, Postgres, MySQL, ClickHouse) have gaps when added, document them here and mark expected diffs in the affected cases via `allowed_diffs`.

## Running Tests

```bash
# All tests (unit + integration)
go test ./replaytest/... -v

# Integration only
go test ./replaytest/... -run TestReplayConsistency_AllCases -v

# Unit tests only
go test ./replaytest/... -run "Test(Normalize|Recursive|Wildcard|Apply|Build|Parse|Write|Has|Capture)" -v

# Race detection
go test ./replaytest/... -race -count=1

# Custom report path
TRPC_AGENT_REPLAY_REPORT_PATH=./my_report.json go test ./replaytest/... -v
```
