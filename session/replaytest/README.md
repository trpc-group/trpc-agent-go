# Replay consistency harness

`replaytest` checks the replay-visible contract shared by Session, Memory,
Summary, and Track backends. It runs the same typed operations against each
backend, normalizes backend-generated values, and compares every result with a
named reference backend or with every other backend in oracle-free consensus
mode.

The public matrix contains 21 cases: single-turn and multi-turn messages, tool
calls, scoped state CRUD and clear semantics, mid-replay Session reload continuity, memory
persistence, ranked memory search, idempotent memory retry recovery, summary
generation/update, summary retained-tail reconstruction, summary filter keys,
tracks, offset-based event pagination, observable Session TTL expiration,
concurrent event branches, and conflict-free concurrent State, Memory,
Summary, and Track writes. Package tests assign one deterministic snapshot
mutation to each case and prove that every mutation produces a blocking diff.
This is normalized Snapshot/Compare mutation coverage, not a claim that all 21
cases inject faults into a durable database. The SQLite submodule separately
executes eight persistence-level fault classes end to end; see its README for
the exact boundary.

Event pagination is exercised through
`session.WithGetSessionEventPage`, not by slicing the final normalized event
list. A backend declares `CapabilityEventPage` only when that public service
operation is implemented. The InMemory and SQLite lightweight adapters leave
it undeclared, so their reports retain an `unsupported_capability` exclusion
for the pagination case while all portable cases continue to run.
Event-page snapshots preserve the global storage order returned by the backend,
even when the complete event set uses causal comparison. A case cannot combine
event pagination with concurrent event appends because offset selection would
depend on their intentionally unspecified global interleaving.

Session TTL is also an observable operation rather than a declaration-only
flag. An adapter declaring `CapabilitySessionTTL` must return the positive TTL
actually configured on its service as `Services.SessionTTL`. The expiration
step first proves that the created Session is visible with the requested
identity, then waits longer than the TTL, subject to a fixed five-second
ceiling, and requires `GetSession` to return no Session. Backends without a
portable TTL configuration leave the capability undeclared and produce
explicit `unsupported_capability` evidence without sleeping.

Replay applies fixed defensive limits. One run accepts at most 64 backends,
1,000 cases, and 1,000 `AllowedDiff` rules per case. One case accepts at most
10,000 steps, 2,000 concurrent branches, and 10,000 generated diffs; one report
accepts at most 100,000 diffs within a 64 MiB encoded-size budget. State keys,
memory entries and searches,
summaries, events, and track events are each capped at 100,000 entries. JSON
nesting is capped at 256 levels. Individual state values are capped at 1 MiB;
event, memory, summary, track payload, and generic JSON values are capped at
8 MiB. A fully encoded comparison snapshot is also capped at 8 MiB, which is
the stricter aggregate boundary even where a domain has a larger cumulative
normalization limit. These limits are part of the harness safety contract;
larger datasets should be partitioned into multiple cases.

Backend, case, step, mode, capability, and other identifier strings are capped
at 4 KiB. JSON Pointer paths are capped at 16 KiB; descriptions, allowed-diff
reasons, and report explanations are capped at 64 KiB. Backend execution errors
are copied into immutable report text, repaired to valid UTF-8, and truncated
to 64 KiB with an explicit suffix. `Report.Validate` applies the same limits to
reports constructed or decoded outside `Runner`.
Arbitrary JSON evidence is also bounded by a graph-node preflight before
reflection, cloning, or marshaling. Report and Diff JSON decoding preserves
numbers as `json.Number`, so integers outside float64's exact range do not drift
across a write/decode/write cycle.

Tool-call extra fields and caller-supplied snapshots are cloned before JSON
encoding. The package-test fault helper uses the same boundary. The harness
permits the known value types `time.Time` and `json.RawMessage`, but rejects
every other value that implements `json.Marshaler` or `encoding.TextMarshaler`
before invoking user code. This conservative boundary prevents mutation,
hidden-state reads, and alias- or capacity-sensitive output from breaking
deterministic replay.

Memory persistence snapshots are content-sorted because `ReadMemories` does not
define cross-backend result order. `StepSearchMemory` is separate: it requires
`CapabilityMemorySearch` and records result order, stable logical memory IDs,
and similarity scores under the step name. Small score drift can be documented
with a path-scoped `AllowedWithinDelta` rule without hiding ranking changes.

State inputs use explicit scopes. Application and user keys are non-empty and
unprefixed; session keys may use `temp:` but not `app:` or `user:`. Every event
state delta is applied to session state, including when the event itself is not
persisted; `app:` and `user:` additionally select their scoped state domains.
Application and user updates may set `Clear: true` to delete the complete
selected scope after applying values. Session clear-all is rejected because
the portable `session.Service` contract does not expose it.
The harness derives those domains and preserved session keys from the replay
input rather than only from stored events, so it does not hide differences in
how adapters apply scoped deltas. Normalized state distinguishes nil, JSON
null, empty bytes, and arbitrary non-JSON bytes with an explicit `nil`, `json`,
or `bytes` kind for every value. JSON objects with duplicate keys also remain
bytes because collapsing them would hide a backend transformation.

Summary retention is intentionally expressed as a portable boundary contract:
the normalized summary records its text, cutoff boundary, and the logical IDs
of events after that boundary that remain part of the current context. The
portable `session.Service` interface does not expose a physical history
truncate operation, so this case does not claim that older events were deleted
from storage. Summary ownership is checked through the isolated session
identity and a fresh-session probe; `Summary` itself has no owner field.

Write recovery is explicit per step. `RecoveryVerify` performs a
read-after-write check after an error and accepts the operation only when the
requested event, state, memory, or track append is observed. Summary recovery
verification is intentionally rejected: summary text is generated by a
backend-owned summarizer, and the harness has no portable expected value with
which to prove that an observed change came from the failed request. Callers
must use `RecoveryNone` for summaries and handle an error as uncertain at the
application boundary.
`RecoveryRetryIdempotent` may retry once after a negative check, but is valid
only for State and Memory because those writes have idempotent service
contracts. Event, Summary, and Track writes are never retried blindly; an
unobserved outcome returns `ErrUncertainCommit`. Event verification is limited
to persisted appends without state deltas so the witness covers the complete
durable effect.

`Step.FailBeforeWrite` explicitly injects a pre-commit error on the initial
attempt and requires a recovery mode. An idempotent retry then calls the actual
backend. The public memory recovery case declares this fault in its steps, so
every adapter exercises the same retry path; case names only identify workloads.

Concurrent event branches require `EventOrderCausal` and use stable internal
execution lanes. A lane is independent of the event's business `filter key`,
so branches may share one. Each concurrent step has one write domain. State,
memory, summary, and track concurrency does not affect event order, but
requires a domain-specific capability and disjoint footprints: state scopes and
keys, memory content, summary filter keys, and track names cannot overlap
across branches. State clear-all, full-session summaries, searches, reloads, nested concurrent
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
| Summary | Distinct, non-empty filter keys | `CapabilityConcurrent` + `CapabilityConcurrentSummary` | Unsupported; errors remain uncertain |
| Track | Distinct track names | `CapabilityConcurrent` + `CapabilityConcurrentTrack` | Verify only |

Anything outside this table is rejected during case validation rather than
being interpreted from backend-specific scheduling or conflict behavior.

`StepReloadSession` explicitly re-fetches the active Session. Later event,
summary, and track writes use that returned value rather than the object created
at the start of the case. The public reload-continuity case writes events and
state on both sides of two reload boundaries, making persistence across an
active replay lifecycle part of the lightweight InMemory/SQLite baseline. A
Session returned by create, reload, recovery, isolation probing, or final
snapshot must match the requested app, user, and session IDs; a mismatch is an
execution failure rather than comparison evidence.

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

Reference mode records an equally explicit comparison manifest: every backend
that produced a snapshot and every completed reference pair with its blocking
and allowed diff counts. `Report.Validate` rejects omitted pairs, unaccounted
backends, and pair counters that disagree with the diff list. This is
structural integrity for runner-generated reports, not a signature or proof of
authenticity for JSON supplied by an untrusted party.

## Additional backends

This package does not register external server-backed adapters. No environment
variable currently enables Redis, PostgreSQL, MySQL, or ClickHouse in this
matrix; setting a database driver's environment variables alone does not add a
backend. Such adapters
belong in the independent `test` module so the root module does not acquire
database drivers or integration-only dependency upgrades. An owning integration
module can register its existing Session
and Memory services through `Backend.Open` and follow the owning module's
existing environment configuration and skip behavior.

Before an external adapter is admitted to a production matrix, its conformance
evidence must cover a unique case namespace; cleanup after success, failure, and
cancellation; two workers writing the same session; read-after-write visibility
for Memory; duplicate delivery of the same logical event; and restart during an
uncertain commit. The lightweight matrix does not claim these properties for an
adapter that has not supplied those tests.

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
