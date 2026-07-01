# Session / Memory / Summary / Track Replay Consistency Harness — Design

- Issue: https://github.com/trpc-group/trpc-agent-go/issues/2001
- Date: 2026-07-01
- Status: Implemented (first pass); remediation approved (see §13)

## 1. Background & Goal

trpc-agent-go supports many Session/Memory backends (InMemory, SQLite, Redis,
Postgres, MySQL, ClickHouse, plus vector-based Memory backends). Teams typically
develop against InMemory and later switch to a persistent backend. If two
backends persist different event ordering, state, memory, or summary data for
the same agent trajectory, production hits replay corruption, lost context,
polluted long-term memory, or wrong summary overwrites.

This project builds a reusable replay-consistency harness: one set of
standardized inputs drives multiple backends, results are read back and
normalized, and a diff report is generated. It is both a test tool and a
quality benchmark for backend implementations. Beyond events and state, it
covers Session Summary generation/save/read/update semantics, and — specific to
the Go repo — Summary `filterKey` scoping and Track observation trajectories.

## 2. Scope & Decisions

Confirmed decisions:

1. **Packaging**: a dedicated Go module `session/replaytest/` with its own
   `go.mod`, wiring sibling backend modules via `replace` (mirrors the existing
   `test/` module). Lightweight mode = InMemory + **real SQLite** (CGO).
   Redis/Postgres/MySQL/ClickHouse are compiled in but runtime-gated by env
   vars.
2. **Case format**: declarative JSON data files under `testdata/cases/`. Fault
   variants live alongside as `*.faulty.json`.
3. **Diff model**: normalize both sides first, then field-by-field compare, then
   filter remaining diffs through an `allowed_diff` rule table (field-path
   whitelist + backend capability declarations for `unsupported`).
4. **Memory backends (lightweight)**: `memory/inmemory` vs real `memory/sqlite`.
5. **Entry point**: `go test` is the primary driver; a small `cmd/replayreport`
   binary generates `session_memory_summary_track_diff_report.json`.
6. **Public cases**: expanded from the required 10 to **20** (10 required + 10
   supplementary across four groups A/B/C/D).

Out of scope (YAGNI): GUI, HTML report, historical trend comparison, memory
backends needing external credentials (mem0/tencentdb), vector backends
(only optionally in integration mode).

## 3. Module Layout

```
session/replaytest/
├── go.mod                       # replace -> ../../ and ../sqlite, ../redis, ...
├── README.md                    # backend onboarding + run instructions
├── DESIGN.md                    # 150–300 word design note (deliverable 5)
├── harness/                     # core engine (unit-testable)
│   ├── model.go                 # ReplayCase / Operation / Snapshot + Views
│   ├── loader.go                # load declarative JSON cases from testdata
│   ├── runner.go                # replay one case to one backend -> Snapshot
│   ├── normalize.go             # normalizer (IDs, timestamps, map order, float, private metadata)
│   ├── compare.go               # field-by-field diff over normalized snapshots
│   ├── allowdiff.go             # allowed_diff rule table + unsupported declarations
│   └── report.go                # generate session_memory_summary_track_diff_report.json
├── backends/
│   ├── registry.go              # construct session/memory services (env-gated)
│   ├── inmemory.go              # root-module inmemory (required)
│   ├── sqlite.go                # real SQLite (required, CGO)
│   └── external.go              # redis/postgres/mysql/clickhouse (env-enabled)
├── testdata/cases/              # >=20 public cases (*.json + *.faulty.json)
├── cmd/replayreport/main.go     # report-generation command
├── consistency_test.go          # go test driver: run cases + assertions
└── harness/*_test.go            # tests for comparator/normalizer/report itself
```

**Data flow**: `loader` reads cases → `runner` executes each case's operation
sequence against every enabled backend (AppendEvent / UpdateSessionState /
AddMemory / CreateSessionSummary / AppendTrackEvent) → reads back and projects
into a unified `Snapshot` → `normalize` → `compare` against the baseline backend
(default `inmemory`) → `allowdiff` filter → `report` aggregates JSON.

Persistent backends are separate modules, imported via `replace` in the
harness `go.mod` (compiled in at build time). Redis/Postgres/MySQL/ClickHouse
are gated **at runtime** by env vars, so lightweight mode stays under 30s.

## 4. Case Format & Snapshot Model

Declarative case (`testdata/cases/*.json`):

```json
{
  "name": "03_tool_call_conversation",
  "description": "user asks -> assistant tool_call -> tool response -> assistant text",
  "key": {"appName": "replaytest", "userID": "u1", "sessionID": "s1"},
  "operations": [
    {"type": "append_event", "event": {"author": "user", "role": "user", "content": "weather?"}},
    {"type": "append_event", "event": {"author": "assistant", "toolCalls": [{"id": "tc1", "name": "get_weather", "args": {"city": "SH"}}]}},
    {"type": "append_event", "event": {"role": "tool", "toolID": "tc1", "content": "sunny", "extensions": {"trpc_agent.tool_call_args": "..."}}},
    {"type": "set_state", "key": "lang", "value": "en"},
    {"type": "add_memory", "content": "user prefers metric units", "topics": ["pref"]},
    {"type": "create_summary", "filterKey": "", "summary": "discussed weather", "force": true},
    {"type": "append_track", "track": "tool_exec", "payload": {"tool": "get_weather", "durationMs": 42, "status": "ok"}}
  ]
}
```

Operation types: `append_event`, `set_state`, `delete_state`, `clear_state`,
`add_memory`, `update_memory`, `delete_memory`, `create_summary`,
`append_track`. Concurrency/out-of-order via a `"concurrent": true` group;
failure recovery via `"repeat": N` or `"failAfter"`.

Unified read-back `Snapshot` (intermediate representation before normalization):

```go
type Snapshot struct {
    Events    []EventView     // author, role, content, toolCalls, toolID, branch, tag, filterKey, stateDelta, extensions
    State     map[string]string
    Memories  []MemoryView    // id, content, topics, kind, score, metadata(kind/eventTime/participants/location)
    Summaries []SummaryView   // filterKey, text, version, sessionID, updatedAt, cutoffAt
    Tracks    []TrackView     // name, payload(canonicalized; volatile durations normalized), timestamp
}
```

Note: the real `memory.Metadata`/`memory.Memory` carry `kind/eventTime/`
`participants/location` (no `scope`), and `session.TrackEvent` carries only
`Track/Payload/Timestamp` (no first-class `eventType/invocationID/error`). Track
sub-fields therefore live inside `payload` and are compared after canonical
normalization.

`runner` reads back through public interfaces (`GetSession` for
events/state/summaries/tracks; `ReadMemories`/`SearchMemories` for memory) and
projects into `Snapshot`. Each `View` keeps only fields the issue's "comparison
must handle" list requires, which naturally hides backend-private structure.

**Locators**: event → `index`; summary → `filterKey`; memory → normalized `id`;
track → `name` + index. Enables precise report location (session id / event
index / summary filter-key / track name / memory id).

## 5. Public Cases (20)

Required 10 (issue mandated):

1. Single-turn plain conversation (user message + assistant text).
2. Multi-turn conversation (ordering on read-back).
3. Tool-call conversation (tool call, tool response, tool call args extension).
4. State updates (write/overwrite/delete/clear within one session).
5. Memory write & read (preferences, facts, task experience, history summary).
6. Summary generation & update (text, filter-key, version, updatedAt, session ownership).
7. Summary + event truncation (retained events + summary + new events restore context).
8. Track events (tool duration, subtask status, error records — cross-backend consistency).
9. Concurrent / out-of-order writes (interleaved tool/sub-agent events; final ordering + normalized result).
10. Failure recovery (mid-write failure, duplicate write, retry — check for duplicate events, dirty state, duplicate memory, wrong summary).

Supplementary 10:

- **A — Go-specific semantics** (highest value; explicitly named in acceptance):
  - 11. Multiple filter-key summaries coexisting (`""`, `user-msgs`, `agent-a/tool`) — no cross-overwrite, correct ownership.
  - 12. Hierarchical filter-key prefix aggregation (`SummaryPrefixCutoff` semantics).
  - 13. Branch / multi-agent trajectory (events with different `branch`/`filterKey`).
  - 14. Event pagination (`WithGetSessionEventPage`) — postgres/mysql only; others `unsupported`.
- **B — State boundaries**:
  - 15. App / User / temp three-level state scope isolation and merge.
  - 16. Binary / Unicode / nil-value state (`[]byte` fidelity; empty vs delete).
- **C — Memory retrieval**:
  - 17. episodic vs fact + retrieval ordering + float similarity tolerance.
  - 18. Memory update causing id rotation (`UpdateResult.MemoryID`) — still aligns after id normalization.
- **D — Event/TTL boundaries**:
  - 19. partial / empty-content event filtering consistency.
  - 20. TTL expiry (integration mode) — cross-backend semantic differences flagged `unsupported`/`allowed_diff`.

Lightweight mode runs the ~18 cases not requiring external services; cases 14
and 20 run for real only in integration mode and are flagged `unsupported` in
lightweight mode. Every case also has a `*.faulty.json` variant carrying an
`expectedDefect` field for precise assertion.

## 6. Normalizer, Comparator & allowed_diff

**Normalizer** (`normalize.go`) — run over each `Snapshot` before comparison:

- **Auto-generated IDs**: event.ID, memory.ID, unrelated summary IDs → stable
  ordinal placeholders (`evt#0`, `mem#0`). Cross-backend alignment relies on
  content + ordinal, not raw IDs.
- **Timestamps**: `Timestamp`/`CreatedAt`/`UpdatedAt`/track times → relative
  order (Nth) or zeroed; keep only the comparable "monotonic increasing"
  property where needed.
- **Map / JSON field order**: state map sorted by key; embedded JSON
  (extensions, tool args, track payload) unmarshaled to `any` then canonically
  reordered.
- **Float**: memory similarity scores and track durations bucketed by tolerance
  (similarity epsilon 1e-6; duration normalized to presence/magnitude, not
  exact value).
- **Backend-private metadata**: dropped by projecting only declared `Snapshot`
  fields.

**Comparator** (`compare.go`): baseline backend (default `inmemory`) vs each
other backend, deep field comparison over normalized snapshots, producing
`Diff{Case, Backend, Category, Locator, FieldPath, BaselineValue, CompareValue}`.
`Category` ∈ {event, state, memory, summary, track}.

**allowed_diff rule table** (`allowdiff.go`): keyed by
`(backend, category, fieldPath-glob)` → `allowed` or `unsupported` + reason.
Raw diffs are filtered through it:

- `unsupported` (e.g. sqlite has no event pagination/TTL) → marked
  `unsupported`, not counted as inconsistency.
- `allowed` (e.g. backend does not backfill `score`; track duration precision) →
  marked `allowed_diff` + explanation.
- unmatched → **real inconsistency**, flagged.

This three-stage pipeline satisfies two acceptance criteria simultaneously:
injected real defects (duplicate event, lost summary, wrong filter-key, dirty
state) fall on fields the normalizer does not touch → 100% detected; legitimate
backend differences are absorbed by the rule table → normal-case false-positive
rate ≤5%.

**Fault detection**: `*.faulty.json` uses the same operations as the normal
case, but the faulty backend injects a defect (drop a summary / alter filterKey
/ duplicate append / tamper state value). These defects land on
normalizer-untouched fields (content, filterKey, count, ownership), so they
necessarily produce a diff unmatched by the rule table.

## 7. Report Format

`session_memory_summary_track_diff_report.json` (generated by `report.go`):

```json
{
  "mode": "light",
  "generatedAt": "2026-07-01T00:00:00Z",
  "baselineBackend": "inmemory",
  "backends": ["inmemory", "sqlite"],
  "summary": {"cases": 20, "compared": 18, "unsupported": 2, "realDiffs": 0, "allowedDiffs": 3},
  "cases": [
    {
      "case": "06_summary_generate_update",
      "sessionID": "s1",
      "results": [
        {
          "backend": "sqlite",
          "category": "summary",
          "locator": {"sessionID": "s1", "summaryFilterKey": "user-msgs"},
          "fieldPath": "summaries[user-msgs].updatedAt",
          "baselineValue": "<ts#2>",
          "compareValue": "<ts#2>",
          "verdict": "allowed_diff",
          "explanation": "updatedAt normalized to relative order; backend clock skew allowed"
        }
      ]
    },
    {
      "case": "06_summary_generate_update.faulty",
      "sessionID": "s1",
      "results": [
        {
          "backend": "sqlite",
          "category": "summary",
          "locator": {"sessionID": "s1", "summaryFilterKey": "user-msgs"},
          "fieldPath": "summaries[user-msgs].text",
          "baselineValue": "discussed weather",
          "compareValue": "<missing>",
          "verdict": "inconsistent",
          "explanation": "summary lost: filter-key present in baseline, absent in compare"
        }
      ]
    }
  ]
}
```

`verdict` ∈ {`consistent`, `allowed_diff`, `unsupported`, `inconsistent`}. Every
result carries a full locator (sessionID + event index / summary filter-key /
memory id / track name) + fieldPath + both values + explanation.

## 8. Run Entry Points & Backend Onboarding

Two entry points:

1. `consistency_test.go` (primary): `go test ./...` runs all cases. For each:
   normal cases must have no `inconsistent`; `*.faulty.json` must hit at least
   the expected `inconsistent` (defect type declared in `expectedDefect`, so the
   assertion is precise, not "any diff"). Lightweight mode ≤30s total.
2. `cmd/replayreport/main.go`:
   `go run ./cmd/replayreport -mode=light -out=session_memory_summary_track_diff_report.json`.

Backend onboarding & gating (`registry.go`):

| Backend | Mode | Enable via |
|---------|------|------------|
| inmemory (session+memory) | lightweight (required) | default |
| sqlite (session+memory) | lightweight (required) | default (real SQLite, CGO) |
| redis | integration | `REPLAYTEST_REDIS_ADDR` |
| postgres | integration | `REPLAYTEST_POSTGRES_DSN` |
| mysql | integration | `REPLAYTEST_MYSQL_DSN` |
| clickhouse | integration | `REPLAYTEST_CLICKHOUSE_DSN` |

Backends without their env var: skipped at registration, absent from report
`backends`, and their cases do not error out. Integration backends are all
compiled in via `replace` but instantiated only when their env var is set.

## 9. Error Handling

- **Backend construction failure** (integration backend unreachable) → warning,
  drop that backend from the run, no panic. Only failure of the two required
  lightweight backends is a hard error.
- **Mid-replay operation failure** (expected in failure-recovery cases) → driven
  by `failAfter`/`repeat`; runner captures and continues to read back, checking
  post-failure consistency rather than crashing.
- **Read-back failure** (backend lacks a query) → classified `unsupported` via
  the allowed_diff table.
- **CGO unavailable** (SQLite cannot compile) → `cmd` and test clearly report
  the need for `CGO_ENABLED=1` (per AGENTS.md).

## 10. Testing Strategy

The comparator/normalizer/report generator must themselves be testable:

- `harness/normalize_test.go`: feed inputs with random IDs, shuffled maps,
  jittered timestamps; assert normalization is stable and equivalence-preserving.
- `harness/compare_test.go`: known baseline vs variant; assert real diffs are
  caught and legitimate differences are absorbed.
- `harness/allowdiff_test.go`: rule-table hit/miss paths.
- `harness/report_test.go`: given a diff list, assert JSON structure, summary
  counts, complete locator fields.
- `consistency_test.go`: end-to-end run of all cases + faulty assertions (the
  gatekeeper for acceptance 2/3/4).

## 11. Deliverables (mapped to issue)

1. Multi-backend replay framework code → `session/replaytest/`.
2. ≥10 public cases → 20 (`testdata/cases/`, with faulty variants).
3. `session_memory_summary_track_diff_report.json` sample → generated by
   `cmd/replayreport` + a committed sample in the repo.
4. Backend onboarding docs → `README.md` (lightweight/integration modes, env table).
5. 150–300 word design note → `DESIGN.md` (normalization, summary comparison,
   track comparison, allowed_diff rules, backend onboarding).
6. Comparator/normalizer/report unit tests → `harness/*_test.go`.

## 12. Acceptance Self-Check

| Acceptance | How met |
|------------|---------|
| 1. InMemory + ≥1 persistent backend | real SQLite |
| 2. Injected inconsistency 100% detected | faulty cases + `expectedDefect` precise assertions |
| 3. Normal-case false positives ≤5% | normalization + allowed_diff rule table |
| 4. Summary loss/overwrite/ownership/filter-key errors 100% detected | group A + summary-specific faulty cases |
| 5. Locate to session/event index/summary filter-key/track/memory | report Locator |
| 6. Lightweight ≤30s; integration env-gated | local-only backends + env skip |

## 13. Remediation Design (approved 2026-07-02)

The first-pass implementation passed its own tests but did not fully honor the
issue's intent. A design review found six gaps; the fixes below are approved.

### 13.1 Real fault injection at the write boundary

Problem: `RunFaulty` replayed a *correct* backend, read it back, then mutated the
in-memory `Snapshot`. Baseline and faulty snapshots both came from the same
correct persistence path, so the harness never proved it can catch a backend
that actually persists wrong data.

Fix: add decorator services in the `backends` package (no harness import → no
import cycle): `faultySession` wraps `session.Service` (+ `session.TrackService`)
and `faultyMemory` wraps `memory.Service`. Each is configured with one fault
kind and injects corruption at the write boundary so the bad data is genuinely
persisted and surfaces on read-back:

| Fault | Real injection point |
|-------|----------------------|
| `duplicate_event` | `AppendEvent` appends twice |
| `drop_memory` | `AddMemory` no-ops |
| `tamper_state` | `UpdateSessionState` / StateDelta corrupted before persist |
| `wrong_track_payload` | `AppendTrackEvent` payload replaced before persist |
| `drop_summary` | `CreateSessionSummary` no-ops |
| `overwrite_summary` / `wrong_summary_session` / `wrong_filterkey` | summary text is produced inside the service, so corruption is applied via a follow-up real write against the persisted summary record — still through the real store, never a snapshot edit |

`RunFaulty` then just calls `Run` against a fault-wrapped backend; the read-back
path is identical to clean runs. The fault-detection test compares a clean
backend of a type versus a fault-wrapped backend of the same type.

### 13.2 Same-domain faulty variants

Realign each faulty variant so the injected fault matches the case's own domain,
and update each `expectedDefect.category/fieldPath/locator` to match:

- `04_state_updates.faulty`: `drop_memory` → `tamper_state`
- `05_memory_write_read.faulty`: `drop_summary` → `drop_memory`
- `08_track_events.faulty`: `wrong_summary_session` → `wrong_track_payload`
- `10_failure_recovery_retry.faulty`: keep `tamper_state`
- `09_concurrent.faulty`: keep `duplicate_event`

### 13.3 Strict expected-defect assertion

`hasExpectedDefect` only checked `Category`. Strengthen it to also require a
`FieldPath` prefix match and a `Locator` match (sessionID + eventIndex /
summaryFilterKey / memoryID / trackName whenever the expected defect specifies
them), guarding the §5 locator-precision acceptance rather than mere category.

### 13.4 Missing scenarios & projection gaps

Runner: implement `clear_state`; wire `Concurrent` (apply marked ops via
goroutines to force out-of-order writes, then deterministic read-back); add
clean cases that actually exercise `update_memory`, `delete_memory`,
`delete_state`, and `failAfter`.

Projection: `projectMemories` also projects episodic metadata
(`kind/eventTime/participants/location`) into `MemoryView.Metadata` so the
existing metadata comparison is real (there is no `scope` field). Track payloads
get volatile-duration/timestamp normalization so they compare deterministically
across backends (track stays payload-based; the API has no first-class fields).

### 13.5 Wire Redis + Postgres (env-gated)

`external.go` builds Redis and Postgres backends only when their env var is set,
else skips that backend. MySQL/ClickHouse keep their entry but are marked
`unsupported` (not wired this pass).

- Redis: `sessionredis.NewService(WithRedisClientURL, WithSummarizer)` +
  `memredis.NewService(WithRedisClientURL)`, gated on `REPLAYTEST_REDIS_ADDR`.
- Postgres: `sessionpg.NewService(WithPostgresClientDSN, WithSummarizer)` +
  `mempg.NewService(WithPostgresClientDSN)`, gated on `REPLAYTEST_POSTGRES_DSN`.

`go.mod` gains `require` + `replace` for `session/redis`, `session/postgres`,
`memory/redis`, `memory/postgres`. README switches from speculative "should
append" language to real env-var instructions.

### 13.6 Sample report with all three verdicts

`RunAll` skipped every faulty case, so the sample report could never contain an
`inconsistent` row. Produce and commit a sample report containing at least one
`allowed_diff`, one `unsupported`, and one `inconsistent` row (the inconsistent
row produced by running a fault-wrapped backend through the same
compare/classify pipeline). Regenerate
`session_memory_summary_track_diff_report.json` accordingly.

Implementation order: 13.1 → 13.2/13.3 → 13.4 → 13.5 → 13.6.
