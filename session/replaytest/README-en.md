# Replay Test — Multi-Backend Replay Consistency Testing Framework

A deterministic, snapshot-based testing framework that drives the same
operation sequence against multiple Session/Memory backends and diffs every
intermediate state. It is designed for production-grade regression checking
with normalization, allowlisted diffs, golden traces, checkpointing, circuit
breakers, and machine-readable reports.

## Design Statement

The framework normalizes backend data to pure-JSON `Snapshot` objects before
comparison, so comparisons are detached from Go struct layout.

- `IDAliasMap` replaces unstable IDs with stable aliases such as `event-000`
  and `tool-call-001`, while preserving cross-reference relationships.
- `nil` values in `StateDelta` are normalized to `MissingValue{}` and
  serialized as `{"__missing":true}`, so "field absent" and "field present
  with null" stay distinguishable.
- Volatile payload keys such as `duration`, `duration_ms`, `elapsed`,
  `elapsed_ms`, `latency`, and `latency_ms` are stripped before comparison.
- Summary comparison converts time-like metadata to event indices for stable
  diffing, and track comparison normalizes per-event payloads.
- `AllowedDiff` rules require exact `section + path` matching, mandatory
  reasons, and section/path consistency.

Code-level constraints that affect every command in this README:

- The test files in this module use `//go:build cgo`.
- SQLite uses `github.com/mattn/go-sqlite3`.

So every test command below should be run with `CGO_ENABLED=1`, including the
InMemory self-verify path.

## Project Structure

```text
session/replaytest/
├── case.go             # Case and operation type definitions
├── normalize.go        # Snapshot normalization
├── diff.go             # Snapshot-first comparison engine
├── harness.go          # Run / RunSuite, capture, checkpoint, report I/O
├── factory.go          # BackendFactory and backend constructors
├── golden.go           # Golden trace save / load / regression helpers
├── types.go            # Shared types
├── cases_test.go       # Public replay regression entry points
├── helpers_test.go     # Test helpers
├── unit_test.go        # Unit and factory coverage tests
├── README-en.md        # This file
├── README-zh.md        # Chinese translation
└── go.mod              # Standalone Go module
```

## Quick Start

Run these commands from the repository root. Each command explicitly changes
into `session/replaytest`, so contributors can copy and paste it directly.

Prerequisites:

- Go `1.24.1` or newer
- a working C toolchain
- `go` already available on `PATH`

Windows examples use PowerShell. macOS and Linux examples use a POSIX shell
such as `bash` or `zsh`.

### Lightweight Mode (Default)

This is the main regression entry point. It runs `TestReplay_All`, which
compares InMemory against SQLite across all 15 public replay cases.

PowerShell:

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -v -count=1 -run TestReplay_All
```

macOS/Linux:

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -v -count=1 -run TestReplay_All
```

### Self-Verify Mode

This runs `TestReplay_Smoke_InMemorySelfVerify`, which compares two isolated
InMemory backends. It still needs `CGO_ENABLED=1` because the package tests are
guarded by the `cgo` build tag.

PowerShell:

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -v -count=1 -run TestReplay_Smoke_InMemorySelfVerify
```

macOS/Linux:

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -v -count=1 -run TestReplay_Smoke_InMemorySelfVerify
```

### Run a Single Case

This example runs only `case01_single_turn` under `TestReplay_All`.

PowerShell:

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -v -count=1 -run "TestReplay_All/case01_single_turn$"
```

macOS/Linux:

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -v -count=1 -run 'TestReplay_All/case01_single_turn$'
```

### Generate a Clean Report

This runs `TestReplay_Report`. The report is written into Go's temporary test
directory, not to a fixed repository path.

PowerShell:

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -v -count=1 -run TestReplay_Report
```

macOS/Linux:

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -v -count=1 -run TestReplay_Report
```

### Generate a Sample Diff Report

This runs `TestReplay_ReportWithDiffs`. The test logs the temporary output
path. A checked-in sample artifact also exists at
`test/session_memory_summary_track_diff_report.json`.

PowerShell:

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -v -count=1 -run TestReplay_ReportWithDiffs
```

macOS/Linux:

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -v -count=1 -run TestReplay_ReportWithDiffs
```

### Run the Full Module Test Suite

PowerShell:

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test ./... -count=1
```

macOS/Linux:

```bash
cd session/replaytest && CGO_ENABLED=1 go test ./... -count=1
```

### Race Detection

PowerShell:

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -race -v -count=1
```

macOS/Linux:

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -race -v -count=1
```

## Backend Integration

### Lightweight Mode

By default, the replay regression path is InMemory vs SQLite:

- **InMemory**: pure in-memory implementation
- **SQLite**: in-memory SQLite via `go-sqlite3`

### Integration / Factory Checks

The code reads backend DSN environment variables in `factory.go`, but the
current public replay entry points do not expose a single "`go test` against
all external backends" command. What is public and directly runnable today is:

- Redis factory verification with in-process `miniredis`
- backend factory / selector tests for Postgres, MySQL, and ClickHouse

#### Redis Factory Without External Redis

This exercises `TestFactory_RedisFactory_Create_WithMiniredis`.

PowerShell:

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; go test . -v -count=1 -run TestFactory_RedisFactory_Create_WithMiniredis
```

macOS/Linux:

```bash
cd session/replaytest && CGO_ENABLED=1 go test . -v -count=1 -run TestFactory_RedisFactory_Create_WithMiniredis
```

#### Postgres Factory Construction

This exercises `TestFactory_PostgresFactory_Create_WithSkipDBInit`.

PowerShell:

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; $env:REPLAY_BACKEND = "postgres"; $env:TRPC_AGENT_REPLAY_POSTGRES_DSN = "postgres://localhost:5432/testdb"; $env:TRPC_AGENT_REPLAY_SKIP_DB_INIT = "1"; go test . -v -count=1 -run TestFactory_PostgresFactory_Create_WithSkipDBInit
```

macOS/Linux:

```bash
cd session/replaytest && REPLAY_BACKEND=postgres TRPC_AGENT_REPLAY_POSTGRES_DSN='postgres://localhost:5432/testdb' TRPC_AGENT_REPLAY_SKIP_DB_INIT=1 CGO_ENABLED=1 go test . -v -count=1 -run TestFactory_PostgresFactory_Create_WithSkipDBInit
```

#### MySQL Factory Construction

This exercises `TestFactory_MysqlFactory_Create_WithSkipDBInit`.

PowerShell:

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; $env:REPLAY_BACKEND = "mysql"; $env:TRPC_AGENT_REPLAY_MYSQL_DSN = "test:test@tcp(localhost:3306)/testdb"; $env:TRPC_AGENT_REPLAY_SKIP_DB_INIT = "1"; go test . -v -count=1 -run TestFactory_MysqlFactory_Create_WithSkipDBInit
```

macOS/Linux:

```bash
cd session/replaytest && REPLAY_BACKEND=mysql TRPC_AGENT_REPLAY_MYSQL_DSN='test:test@tcp(localhost:3306)/testdb' TRPC_AGENT_REPLAY_SKIP_DB_INIT=1 CGO_ENABLED=1 go test . -v -count=1 -run TestFactory_MysqlFactory_Create_WithSkipDBInit
```

#### ClickHouse Factory Construction

This exercises `TestFactory_ClickhouseFactory_Create_WithSkipDBInit`.

PowerShell:

```powershell
Set-Location session/replaytest; $env:CGO_ENABLED = "1"; $env:REPLAY_BACKEND = "clickhouse"; $env:TRPC_AGENT_REPLAY_CLICKHOUSE_DSN = "clickhouse://localhost:9000"; $env:TRPC_AGENT_REPLAY_SKIP_DB_INIT = "1"; go test . -v -count=1 -run TestFactory_ClickhouseFactory_Create_WithSkipDBInit
```

macOS/Linux:

```bash
cd session/replaytest && REPLAY_BACKEND=clickhouse TRPC_AGENT_REPLAY_CLICKHOUSE_DSN='clickhouse://localhost:9000' TRPC_AGENT_REPLAY_SKIP_DB_INIT=1 CGO_ENABLED=1 go test . -v -count=1 -run TestFactory_ClickhouseFactory_Create_WithSkipDBInit
```

### Environment Variables

These are the environment variables actually read by the current module:

| Variable | Used By | Meaning |
| --- | --- | --- |
| `REPLAY_BACKEND` | `ResolvePair` | Selects the target backend for factory-related helpers and unit tests. Supported values: `inmemory`, `sqlite`, `miniredis`, `redis`, `postgres`, `mysql`, `clickhouse`. |
| `TRPC_AGENT_REPLAY_REDIS_URL` | `redisFactory`, `ResolveBackends` | Redis connection URL for constructing a real Redis backend. |
| `TRPC_AGENT_REPLAY_POSTGRES_DSN` | `postgresFactory`, `ResolveBackends` | PostgreSQL DSN for constructing a real Postgres backend. |
| `TRPC_AGENT_REPLAY_MYSQL_DSN` | `mysqlFactory`, `ResolveBackends` | MySQL DSN for constructing a real MySQL backend. |
| `TRPC_AGENT_REPLAY_CLICKHOUSE_DSN` | `clickhouseFactory`, `ResolveBackends` | ClickHouse DSN for constructing a real ClickHouse backend. |
| `TRPC_AGENT_REPLAY_SKIP_DB_INIT` | `postgresFactory`, `mysqlFactory`, `clickhouseFactory` | Skips DB initialization during factory creation when set. |

What does **not** exist in the current code:

- `REPLAY_BACKEND` does not change `TestReplay_All`.
- `TRPC_AGENT_REPLAY_REPORT_PATH` is not implemented.
- There is no public replay command in this module that runs the full replay
  matrix against Redis/Postgres/MySQL/ClickHouse only by exporting DSNs.

## 15 Replay Cases

`TestReplay_All` and `TestReplay_Smoke_InMemorySelfVerify` both execute the
same 15 public cases:

| # | Case | Required Caps | Coverage |
| --- | --- | --- | --- |
| 1 | `case01_single_turn` | `events` | Create session, append user and assistant events, read session back |
| 2 | `case02_multi_turn` | `events` | Ten-event multi-turn sequence integrity |
| 3 | `case03_tool_call_cross_ref` | `events` | Tool call / tool response cross-reference normalization |
| 4 | `case04_state_update_overwrite_delete` | `state` | State create, overwrite, append, delete-by-`nil` |
| 5 | `case05_memory_search_and_score` | `memory` | Memory insertion, metadata, unordered comparison |
| 6 | `case06_summary_filter_and_update` | `summary` | Summary creation for default and branch filter keys |
| 7 | `case07_summary_event_window_recovery` | `summary`, `events` | Summary boundary recovery with allowed backend diffs |
| 8 | `case08_track_status_and_error` | `track` | Track event payload comparison with volatile fields removed |
| 9 | `case09_concurrent_tool_interleaving` | `events` | Count-only event comparison for interleaving scenarios |
| 10 | `case10_failure_recovery_without_duplicates` | `events`, `summary` | Duplicate appends, overwrite retry, idempotent summary |
| 11 | `case11_state_delta_null` | `state`, `events` | `nil` `StateDelta` normalization to `MissingValue` |
| 12 | `case12_boundary_and_error` | `events`, `state` | Empty state, extensions, branch/tag/filterKey, boundary reads |
| 13 | `case13_state_delete` | `state` | Delete a state key by writing `nil` |
| 14 | `case14_state_scopes` | `state` | App-scoped and user-scoped state capture |
| 15 | `case15_summary_filter_key` | `summary`, `events` | Summary generation scoped to a specific `filterKey` |

## Capabilities

| Capability | Description |
| --- | --- |
| `events` | Can store and retrieve session events |
| `state` | Can store and retrieve session state |
| `memory` | Can store and retrieve memory entries |
| `summary` | Can create and retrieve session summaries |
| `track` | Can append and retrieve track events |
| `event_state_delta_null` | Supports `nil` values in `StateDelta` |

When a backend lacks a required capability, that case is skipped for the
backend. If fewer than two valid backends remain, the result becomes
`inconclusive` instead of `pass`.

## Diff Severity

| Severity | Condition | Example |
| --- | --- | --- |
| `critical` | Data loss or missing section | `MissingValue` vs value, event missing in one backend |
| `major` | Value mismatch | Wrong content or wrong key |
| `minor` | Allowed difference | Known backend difference covered by `AllowedDiff` |

## Report Format

The checked-in sample report is:

- `test/session_memory_summary_track_diff_report.json`

The test that generates a sample diff report is `TestReplay_ReportWithDiffs`.
That test writes the generated JSON into a temporary test directory and logs
the output path.

`Report` includes:

- top-level run metadata such as `report_id`, `version`, and `run_id`
- the backend list
- per-case results under `cases`
- aggregate counters under `summary`
- per-diff severity plus context such as `path`, `section`, and backend values

`ReadReportWithVerify` verifies the checksum sidecar and rejects corrupted
files.

## Acceptance Results

The following commands were re-run and passed while updating this README:

| Command / Test | Result |
| --- | --- |
| `TestReplay_All` | PASS |
| `TestReplay_Smoke_InMemorySelfVerify` | PASS |
| `TestReplay_Report` | PASS |
| `TestReplay_ReportWithDiffs` | PASS |
| `TestFactory_RedisFactory_Create_WithMiniredis` | PASS |
| `TestFactory_PostgresFactory_Create_WithSkipDBInit` | PASS |
| `TestFactory_MysqlFactory_Create_WithSkipDBInit` | PASS |
| `TestFactory_ClickhouseFactory_Create_WithSkipDBInit` | PASS |
| `go test ./... -count=1` | PASS |
| `go test . -race -v -count=1` | PASS |

### Acceptance Criteria Verification

| Criterion | Result | Notes |
| --- | --- | --- |
| Module-local commands can be copied directly | PASS | Commands now enter `session/replaytest` explicitly before running `go test` |
| PowerShell commands are valid | PASS | Re-run during README update |
| macOS/Linux shell commands are valid | PASS | Re-run during README update |
| English and Chinese README commands are aligned | PASS | The same command set is documented in both files |
| README commands match current public test entry points | PASS | Only documented commands with verified test names remain |

## BackendFactory Interface

```go
type BackendFactory interface {
    Kind() string
    Capabilities() Capabilities
    Create(ctx context.Context, t *testing.T) *Backend
}
```

To add a new backend, implement the interface and register it in
`ResolveBackends` or `ResolvePair`.
