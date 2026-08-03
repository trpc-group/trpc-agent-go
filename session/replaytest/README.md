# Replay consistency harness

`replaytest` checks the replay-visible contract shared by Session, Memory,
Summary, and Track backends. It runs the same typed operations against each
backend, normalizes backend-generated values, and compares every result with a
named reference backend or with every other backend in oracle-free consensus
mode.

The public matrix contains 17 cases: single-turn and multi-turn messages, tool
calls, scoped state CRUD, mid-replay Session reload continuity, memory
persistence, ranked memory search, idempotent memory retry recovery, summary
generation/update, summary retained-tail reconstruction, summary filter keys,
tracks, concurrent event branches, and conflict-free concurrent State, Memory,
Summary, and Track writes. Each case names an injected fault; the unit test
proves that every fault produces a blocking diff.

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
or `bytes` kind for every value. JSON objects with duplicate keys also remain
bytes because collapsing them would hide a backend transformation.

Write recovery is explicit per step. `RecoveryVerify` performs a
read-after-write check after an error and accepts the operation only when the
requested event, state, memory, summary change, or track append is observed.
`RecoveryRetryIdempotent` may retry once after a negative check, but is valid
only for State and Memory because those writes have idempotent service
contracts. Event, Summary, and Track writes are never retried blindly; an
unobserved outcome returns `ErrUncertainCommit`. Event verification is limited
to persisted appends without state deltas so the witness covers the complete
durable effect.

Concurrent event branches require `EventOrderCausal` and use stable internal
execution lanes. A lane is independent of the event's business `filter key`,
so branches may share one. Each concurrent step has one write domain. State,
memory, summary, and track concurrency does not affect event order, but
requires a domain-specific capability and disjoint footprints: state scopes and
keys, memory content, summary filter keys, and track names cannot overlap
across branches. Full-session summaries, searches, reloads, nested concurrent
steps, and event state deltas remain sequential
because they have no portable conflict-free contract. Backends may omit
`CapabilityConcurrentState`, `CapabilityConcurrentMemory`,
`CapabilityConcurrentSummary`, or `CapabilityConcurrentTrack` when their
implementation cannot prove the corresponding atomicity.

The portable write contract is deliberately narrower than any one backend's
implementation:

| Domain | Concurrent footprint | Required capability | Recovery after an error |
| --- | --- | --- | --- |
| Event | Persistable, state-delta-free events in ordered lanes | `CapabilityConcurrent` | Verify only |
| State | Disjoint scope and key pairs | `CapabilityConcurrent` + `CapabilityConcurrentState` | Verify or retry once |
| Memory | Distinct memory content | `CapabilityConcurrent` + `CapabilityConcurrentMemory` | Verify or retry once |
| Summary | Distinct, non-empty filter keys | `CapabilityConcurrent` + `CapabilityConcurrentSummary` | Verify only |
| Track | Distinct track names | `CapabilityConcurrent` + `CapabilityConcurrentTrack` | Verify only |

Anything outside this table is rejected during case validation rather than
being interpreted from backend-specific scheduling or conflict behavior.

`StepReloadSession` explicitly re-fetches the active Session. Later event,
summary, and track writes use that returned value rather than the object created
at the start of the case. The public reload-continuity case writes events and
state on both sides of two reload boundaries, making persistence across an
active replay lifecycle part of the lightweight InMemory/SQLite baseline.

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

This package does not register external server-backed adapters. Such adapters
belong in the independent `test` module so the root module does not acquire
database drivers or integration-only dependency upgrades. Future Redis,
PostgreSQL, MySQL, and ClickHouse adapters can register their existing Session
and Memory services through `Backend.Open` and follow the owning module's
existing environment configuration and skip behavior.

An adapter must isolate test data, clean up sessions, scoped state, summaries,
tracks, and memories, and declare only capabilities that it actually wires.
Missing environment variables should skip the integration rather than weaken
the lightweight matrix. Capabilities such as ranked Memory search must remain
undeclared when the backend does not share a portable scoring contract. The
report then retains explicit `unsupported` evidence for cases outside the
adapter's declaration.

`AllowedDiff` rules are deliberately strict: an unordered backend pair, JSON
Pointer glob, known rule, and a non-empty explanation are mandatory. Pairwise
agreement is based on blocking differences, so an explicitly allowed
difference does not create a false outlier.
