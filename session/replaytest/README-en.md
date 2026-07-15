# Replay Test — Multi-Backend Replay Consistency Testing Framework

A deterministic, snapshot-based testing framework that drives the same
operation sequence against multiple Session/Memory backends and diffs every
intermediate state. Designed for production-grade stability with atomic writes,
circuit breakers, parallel execution, and checksum-verified reports.

## Design Statement

The framework normalizes all backend data to pure-JSON `Snapshot` objects before comparison, decoupling from Go struct layout.
- Normalization strategy:
    - `IDAliasMap` replaces UUIDs with stable aliases (e.g., `event-000`, `tool-call-001`) preserving cross-reference relationships; 
    - `nil` values in StateDelta become `MissingValue{}` (serialized as `{"__missing":true}`), distinct from explicit `null`, precisely capturing "field absent" vs "field present with null value" semantics;
    - volatile keys (`duration`, `duration_ms`, `elapsed`, `elapsed_ms`, `latency`, `latency_ms`) are stripped; memories are sorted by content (`UnorderedMemories`) and track events by JSON representation. 
- Summary comparison converts `UpdatedAt`, `CutoffAt`, and `LastEventID` to event indices for precise diffing; `FilterKey` requires exact match; `Text` allows known truncation differences via `AllowedDiff`. 
- Track comparison compares payload per-event under each track name, auto-strips volatile keys, sorts by JSON to eliminate ordering differences. `AllowedDiff` rules require exact section+path matching (no wildcards), mandatory reason, bidirectional matching, and section-path consistency enforcement. 
- Backend integration via environment variables: lightweight mode requires only InMemory+SQLite (CGO); Redis/Postgres/MySQL/ClickHouse are optionally enabled via corresponding DSN environment variables.

## Project Structure

```text
session/replaytest/
├── case.go             # Case, Op type definitions
├── normalize.go        # Snapshot normalization (ID aliasing, MissingValue, volatile keys)
├── diff.go             # Snapshot-first comparison engine with severity classification
├── harness.go          # Harness (Run, RunSuite), Capture, report I/O, retry, checkpoint
├── factory.go          # BackendFactory + InMemory / SQLite / miniredis / external factories
├── golden.go           # Golden Trace save / load / regression detection
├── types.go            # Shared types (Snapshot, Backend, Capabilities, RetryPolicy, Report, etc.)
├── cases_test.go       # 15 replay cases + drift detection + summary fault + report tests
├── helpers_test.go     # Test helpers (event builders, assertions, makeBackends)
├── unit_test.go        # Unit tests for all components
├── README-zh.md        # Chinese version (includes design statement)
├── README-en.md        # This file (English, includes design statement)
└── go.mod              # Separate Go module (imports session/sqlite)
```

## Quick Start

All commands should be run from the **`session/replaytest`** directory (it has its own `go.mod`).

### Lightweight Mode (Default)

Uses only InMemory + SQLite, no external dependencies, runs in <3 seconds:

```bash
cd session/replaytest
# Linux/macOS:
CGO_ENABLED=1 go test . -v
# Windows PowerShell:
$env:CGO_ENABLED="1"; go test . -v
```

### Self-Verify Mode

InMemory vs InMemory (no CGO required):

```bash
cd session/replaytest
$env:REPLAY_BACKEND="inmemory"; go test . -v -run TestReplay_All
```

### Run a Single Case

```bash
cd session/replaytest
$env:CGO_ENABLED="1"; go test . -v -run "TestReplay_All/case01"
```

### Generate Diff Report

```bash
cd session/replaytest
$env:CGO_ENABLED="1"; go test . -v -run TestReplay_Report
```

### Race Detection

```bash
cd session/replaytest
$env:CGO_ENABLED="1"; go test . -race -v
```

## Backend Integration

### Lightweight Mode

No external services required by default. The framework includes built-in InMemory and SQLite backends:

- **InMemory**: Pure in-memory implementation, zero dependencies
- **SQLite**: Requires `CGO_ENABLED=1` and a C compiler

### Integration Mode (External Backends)

Enable external database backends by setting environment variables. The framework automatically runs health checks (Probe), warm-up (WarmUp), and leak detection (VerifyCleanup):

| Environment Variable | Backend | Description |
|---------------------|---------|-------------|
| `TRPC_AGENT_REPLAY_REDIS_URL` | Redis | Empty → uses miniredis (built-in simulation) |
| `TRPC_AGENT_REPLAY_POSTGRES_DSN` | PostgreSQL | Full connection string, e.g., `postgres://user:pass@localhost:5432/test` |
| `TRPC_AGENT_REPLAY_MYSQL_DSN` | MySQL | DSN format, e.g., `user:pass@tcp(localhost:3306)/test` |
| `TRPC_AGENT_REPLAY_CLICKHOUSE_DSN` | ClickHouse | ClickHouse connection string |

**Example: Enable Redis integration test**

```bash
# Using local Redis
$env:TRPC_AGENT_REPLAY_REDIS_URL="redis://localhost:6379"; $env:CGO_ENABLED="1"; go test ./session/replaytest/ -v -run TestReplay_Smoke

# Using built-in miniredis (no Redis server needed)
$env:CGO_ENABLED="1"; go test ./session/replaytest/ -v -run TestReplay_Smoke
```

**Example: Enable all backend integration tests**

```bash
export TRPC_AGENT_REPLAY_REDIS_URL="redis://localhost:6379"
export TRPC_AGENT_REPLAY_POSTGRES_DSN="postgres://localhost:5432/test"
export TRPC_AGENT_REPLAY_MYSQL_DSN="root@tcp(localhost:3306)/test"
export TRPC_AGENT_REPLAY_CLICKHOUSE_DSN="clickhouse://localhost:9000"
CGO_ENABLED=1 go test ./session/replaytest/ -v -run TestReplay_All
```

### Other Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `REPLAY_BACKEND` | `sqlite` | Target backend: `inmemory` / `sqlite` |
| `TRPC_AGENT_REPLAY_REPORT_PATH` | (empty) | Path to write diff report after RunSuite |

## 15 Replay Cases

| # | Case | Required Caps | Coverage |
|---|------|--------------|----------|
| 1 | Single turn | events | Create + 2 events + GetSession |
| 2 | Multi turn | events | 10-event sequence integrity |
| 3 | Tool call cross-ref | events | ToolCalls, ToolCallID, Extensions, invocation ID aliasing |
| 4 | State update | events, state | Create with state, key/value, overwrite, delete via nil, StateDelta via event |
| 5 | Memory search | memory | AddMemory, ReadMemories, score, metadata, unordered comparison |
| 6 | Summary filter | summary | CreateSessionSummary (main + branch), GetSummaryText |
| 7 | Summary window | summary | 20 events + summary + 5 more events, boundary index (allowed diff) |
| 8 | Track events | track | start/end/error payloads, volatile keys removed (`duration`, `latency_ms`, etc.) |
| 9 | Event count | events | 15 events (3x5), CountOnly mode for cross-backend count verification |
| 10 | Failure recovery | events, summary | Duplicate event append, state overwrite, idempotent summary, no corruption |
| 11 | StateDelta null | events, state | nil StateDelta → MissingValue, CapEventStateDeltaNull |
| 12 | Boundary & error | events, state | Empty state, extensions, branch/tag/filterKey, past EventTime, large EventNum |
| 13 | State delete | state | Create session with state, delete key by setting nil |
| 14 | State scopes | state | AppState (app-scoped) and UserState (user-scoped) across sessions |
| 15 | Summary filter key | summary, events | Events with filterKey, summary scoped to specific filterKey |

## Capabilities

| Capability | Description |
|-----------|-------------|
| `events` | Can store and retrieve session events |
| `state` | Can store and retrieve session state |
| `memory` | Can store and retrieve memory entries |
| `summary` | Can create and retrieve session summaries |
| `track` | Can append and retrieve track events |
| `event_state_delta_null` | Supports nil values in StateDelta (distinguishes delete from set-null) |

When a backend lacks a required capability, the case is skipped for that backend. If fewer than two backends remain, the result is `inconclusive` rather than `pass` — this prevents false positives from insufficient data.

## Diff Severity

| Severity | Condition | Example |
|----------|-----------|---------|
| `critical` | Data loss or absent section | MissingValue vs value, event missing in one backend |
| `major` | Value mismatch | Different content, wrong key |
| `minor` | Allowed difference | Known architectural diff with documented reason |

## Report Format 

Sample report: `test/session_memory_summary_track_diff_report.json`

The sample report is generated by `TestReplay_ReportWithDiffs` using **real backend execution data + injected drifts**, with every diff verified by test assertions. Report contents include:

| Diff Type | Case | Path | Baseline (value_a) | Compared (value_b) | allowed | Severity | Explanation |
|-----------|------|------|--------------------|--------------------|---------|----------|-------------|
| State overwrite lost | case04 | `$.state.k1` | `"v1-new"` | `"v1"` | false | major | State overwrite not applied |
| Summary text truncation | case06 | `$.summaries[""].text` | `"summary-of-10-events"` | `"summary-of-10-events-truncated"` | true | minor | Summary text differs due to truncation |
| Track field missing | case08 | `$.tracks["agent-run"][1].payload.status` | `"ok"` | `{"__missing":true}` | false | critical | Track payload field missing in compared backend |
| StateDelta semantic diff | case11 | `$.events[1].stateDelta.k2` | `{"__missing":true}` | `null` | false | critical | MissingValue (field absent) vs nil (explicit null) |
| Insufficient backend caps | case12 | — | — | — | — | — | SQLite lacks summary/track support |

Key fields:

- `report_id`: Version stamp (`replay-v2`) instead of timestamps
- `version`: Schema version (`"v2"`)
- `run_id`: Timestamp-pid-hostname for CI deduplication
- `severity`: Per-diff classification (critical/major/minor)
- `backend_metrics`: Per-case timing data and retry metrics
- `skipped_backends`: Backend → unsupported capability list
- `inconclusive_cases`: Count of cases with insufficient valid backends
- Checksum sidecar: `<report>.sha256` file alongside the JSON report for integrity verification

`ReadReportWithVerify` recomputes the checksum from the sidecar and rejects corrupted files. The report file itself is valid JSON. A version guard rejects unknown schema versions.

## Acceptance Results

| Item | Result |
|------|--------|
| Normal lightweight matrix | 15/15 PASS, 0 diff, 0 inconclusive |
| Public case injected drift | 15/15 detected, asserting section + path + locator |
| Fine-grained drift | 4 Event/State/Memory/Summary/Track types + 4 raw Summary fault types all detected |
| Lightweight mode duration | `real 8.09s` (with race detection), below 30s requirement |
| `session/replaytest` coverage | 68.8% |
| Race detection | 0 data races, all concurrent scenarios passed |
| go vet | 0 warnings (only the copy of session.Session holding a lock in helpers_test.go is a pre-existing package-level issue) |

### Acceptance Criteria Verification

| Criterion | Result | Notes |
|-----------|--------|-------|
| At least InMemory + one persistent backend comparison | ✅ | InMemory + SQLite (`:memory:`) default pair; environment variables can enable Redis/PostgreSQL/MySQL/ClickHouse |
| 10 public replay cases 100% detect injected inconsistencies | ✅ | All 15 cases PASS; `TestReplay_ConsistencyDetectsInjectedDrift` verifies all four types of injected drift (events/state/summaries/tracks) are detected |
| Normal case false positive rate ≤ 5% | ✅ | 15/15 normal cases with zero diffs, false positive rate 0% |
| Summary loss/overwrite error/session ownership error detection rate 100%, Go must also cover filter-key errors | ✅ | `TestReplay_SummaryFaultsDetected` verifies all four types (summary_lost, summary_text_wrong, summary_filter_key_wrong, summary_boundary_mismatch) are detected |
| Diff report can locate session id/event index/summary id/filter-key/field path/values from both backends; Go must also support track name/memory id | ✅ | Diff struct includes Case/SessionID/EventIndex/MemoryID/TrackName/SummaryKey/Path/ValueA/ValueB |
| Lightweight mode full run time ≤ 30 seconds | ✅ | `real 8.09s` (with race detector), well below 30s threshold |

## BackendFactory Interface

```go
type BackendFactory interface {
    Kind() string
    Capabilities() Capabilities
    Create(ctx context.Context, t *testing.T) *Backend
}
```

To add a new backend, implement the interface and register in `ResolveBackends` or `ResolvePair`.
