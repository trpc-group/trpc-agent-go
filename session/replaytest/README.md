# Replay consistency harness

`replaytest` checks the replay-visible contract shared by Session, Memory,
Summary, and Track backends. It runs the same typed operations against each
backend, normalizes backend-generated values, and compares every result with a
named reference backend or with every other backend in oracle-free consensus
mode.

The public matrix contains 12 cases: single-turn and multi-turn messages, tool
calls, scoped state CRUD, memory persistence, ranked memory search, idempotent
memory retry recovery, summary generation/update, summary retained-tail
reconstruction, summary filter keys, tracks, and concurrent branch writes.
Each case names an injected fault; the unit test proves that every fault
produces a blocking diff.

Memory persistence snapshots are content-sorted because `ReadMemories` does not
define cross-backend result order. `StepSearchMemory` is separate: it requires
`CapabilityMemorySearch` and records result order, stable logical memory IDs,
and similarity scores under the step name. Small score drift can be documented
with a path-scoped `AllowedWithinDelta` rule without hiding ranking changes.

State inputs use explicit scopes. Application and user keys are non-empty and
unprefixed; session keys may use `temp:` but not `app:` or `user:`. Every event
state delta is applied to session state, including when the event itself is not
persisted; `app:` and `user:` additionally select their scoped state domains.
The harness derives those domains and preserved session keys from the replay
input rather than only from stored events, so it does not hide differences in
how adapters apply scoped deltas. Normalized state distinguishes nil, JSON
null, empty bytes, and arbitrary non-JSON bytes with an explicit `nil`, `json`,
or `bytes` kind for every value.

Concurrent steps support event-only branches with stable internal execution
lanes and require `EventOrderCausal`. A lane is independent of the event's
business `filter key`; branches may use the same filter key. This models
interleaved tool or sub-agent events without claiming portable concurrent
state, memory, summary, or track write semantics that the backend interfaces do
not provide. Summaries cannot be combined with concurrent steps in one case.

## Run

Root-module tests use two isolated InMemory services:

```bash
go test ./session/replaytest -count=1
```

The SQLite adapter is a separate module so the root module does not acquire a
CGO build requirement:

```bash
cd session/replaytest/sqlite
CGO_ENABLED=1 go test ./... -run TestLightweightReplayMatrix -count=1
```

`Runner.Run` returns a `Report`. Use `WriteReport` to emit the JSON artifact.
An example is available at
`testdata/session_memory_summary_track_diff_report.json`.

Reference mode is the zero-value default and remains convenient for two
backends:

```go
report, err := (replaytest.Runner{Reference: "inmemory"}).Run(ctx, cases, backends)
```

For three or more independent implementations, consensus mode avoids assuming
that the reference is correct:

```go
report, err := (replaytest.Runner{Mode: replaytest.ComparisonConsensus}).Run(ctx, cases, backends)
```

Consensus mode compares every backend pair in stable name order. It reports a
single `outlier` only when that backend disagrees with every other backend and
all remaining backends agree with each other. Two-backend disagreements,
split votes, and non-transitive results are `ambiguous`; fewer than two
successful comparable backends are `insufficient`. Execution errors and
unsupported capabilities stay outside the consensus matrix and remain visible
as ordinary report diffs.

## Additional backends

Optional server-backed adapters are not registered by this package. To add
one, keep its integration test in the owning Redis, PostgreSQL, MySQL, or
ClickHouse module and wrap the existing `session.Service` and `memory.Service`
implementations in a `Backend` factory. Follow that module's existing
integration-test configuration and skip behavior so credentials and external
services remain optional. Missing capabilities are reported as `unsupported`
allowed diffs instead of silently skipping assertions.

`AllowedDiff` rules are deliberately strict: an unordered backend pair, JSON
Pointer glob, known rule, and a non-empty explanation are mandatory. Pairwise
agreement is based on blocking differences, so an explicitly allowed
difference does not create a false outlier.
