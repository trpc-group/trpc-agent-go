# Replay Consistency

Applications usually develop against the in-memory Session and Memory backends
and switch to SQLite, Redis, or a SQL database later. When backends disagree
about event order, state, memory, or summaries, the failures surface far from
their cause: replay reorders, context is lost, long-term memory is polluted,
and summaries overwrite one another.

The replay consistency harness turns that class of bug into a test. It drives
one deterministic operation script against several backends, projects what each
one persisted into a canonical form, and reports every field that differs.

The harness lives in the `test` module at
[`test/replayconsistency`](https://github.com/trpc-group/trpc-agent-go/tree/main/test/replayconsistency).

## Running it

Lightweight mode needs no external service and is what CI runs:

```bash
cd test
go test ./replayconsistency/
```

Three backends take part: in-memory, SQLite on a temporary file, and Redis
against an in-process server. In-memory is the baseline the others are compared
against, because it is the implementation applications start from.

Regenerate the sample diff report:

```bash
go test ./replayconsistency/ -run TestReport -update-report
```

The artifact is written to
`test/replayconsistency/testdata/session_memory_summary_track_diff_report.json`.

### Integration mode

Integration backends are enabled by environment variables and skipped when
unset:

| Variable | Backend | Example |
| --- | --- | --- |
| `TRPC_REPLAY_REDIS_URL` | Real Redis server | `redis://127.0.0.1:6379` |

```bash
TRPC_REPLAY_REDIS_URL=redis://127.0.0.1:6379 go test ./replayconsistency/
```

Each run uses a fresh key prefix, so replaying against a shared server cannot
observe another run's data.

Adding a backend takes one entry in `IntegrationBackends` and a `require` line
in `test/go.mod`. Backends that live in this repository are separate modules, so
they also need a `replace` directive pointing at the local checkout:

```bash
cd test
go mod edit -replace=trpc.group/trpc-go/trpc-agent-go/session/postgres=../session/postgres
go mod tidy
```

Without the `replace`, the harness resolves a published version and compares
the current checkout against a release rather than against itself. The cases
and the comparator need no change either way, because they depend only on
`session.Service` and `memory.Service`.

## Cases

| Case | What it exposes |
| --- | --- |
| `single-turn` | One user message and one assistant reply |
| `multi-turn-ordering` | Read-back order across three turns |
| `tool-call-round-trip` | Tool call, tool response, arguments, extensions |
| `state-write-overwrite-clear` | Session, app and user state; nil versus delete |
| `memory-write-update-delete` | Identifier rotation on content versus topic change |
| `summary-generate-and-update` | Whole-session and branch summaries, regeneration |
| `summary-with-event-truncation` | A summary covering early turns, raw events after |
| `track-events` | Timing, status and error entries across two tracks |
| `interleaved-out-of-order-writes` | Two invocations appended out of timestamp order |
| `retry-and-recovery` | A replayed event, memory write and summary after a retry |

Every case begins with a user message, and that is a requirement rather than a
style choice. `session.ApplyEventFiltering` runs on every append and anchors the
event list at a user message: events before the first user message are
truncated, and a session whose events contain no user message reads back empty.
A case built only from assistant events therefore observes nothing on every
backend, and uniform emptiness is indistinguishable from agreement.

## Design

**Normalization.** Specs carry their own event identifiers and express time as
an offset from a per-run base instant, which removes the two largest sources of
nondeterminism before comparison begins. One base is shared by every backend so
all of them receive identical absolute timestamps. Timestamps are compared as
offsets truncated to milliseconds, JSON payloads and tool arguments are
re-encoded with sorted keys, maps are projected as key-ordered slices, and
set-like lists such as topics are sorted. The base leads the wall clock by a
minute because backends may discard data predating the session it belongs to.

**Comparison.** The comparator walks the projection by reflection rather than by
hand-written field comparisons, so a field added to the projection participates
automatically instead of being silently skipped. Elements identified by a key,
such as summaries by filter key or memories by identifier, are matched by that
key so a missing element is reported as missing rather than shifting everything
after it. Events and track entries are matched by position, because their order
is part of the contract. Divergences carry a path such as
`sessions[ref="app/u1/s1"].summaries[filterKey="tool"].text`.

**Classification.** A difference is either allowed, known, or fatal. Allowed
means both backends are entitled to it; that list is currently empty, which is
the intended state, because nothing observed so far deserves to be blessed as
behavior. Known means the difference is real and recorded with evidence rather
than failing the build while the question is open. Anything else fails. A value
that is nondeterministic by construction, such as the order `ReadMemories`
returns when writes tie on the wall clock, is excluded from the projection with
a documented tag rather than compared and then forgiven, so the report stays
reproducible. Summaries also project the session they surfaced under, so a
summary attributed to the wrong session is visible rather than merely absent.

**Self-verification.** Fault injection corrupts one backend on purpose and
requires the comparator to notice: dropped, duplicated and reordered events,
lost summaries, summaries filed under the wrong filter key or the wrong
session, lost and leaked state keys, and lost track entries. Without it the
suite could only show that backends currently agree, which is also what a
comparator that inspects nothing would report.

## Known divergences

The lightweight run currently records these. They are reported in the artifact
and do not fail the build.

- **Out-of-order appends replay in a different order.** Events appended out of
  timestamp order read back in timestamp order on Redis and in append order on
  in-memory and SQLite. The Redis path indexes events with
  `ZADD <key> <timestamp> <eventID>` and reads that sorted set by score. This is
  ordinary Redis sorted-set behavior, not an artifact of the in-process server.
- **A retried event collapses on Redis.** Re-appending an event whose identifier
  was already stored overwrites it, because events are stored with
  `HSET <key> <eventID> <json>`; SQLite has no uniqueness constraint on the
  identifier and in-memory appends unconditionally. `session.Service` does not
  document whether `AppendEvent` is idempotent, so neither behavior violates a
  stated contract.
- **A nil state delta leaves the previous value on Redis.** Observed against the
  in-process server, whose Lua decodes JSON null to a Lua nil; a Lua table
  cannot hold a nil value, so the guard in the delta script sees an empty table
  and skips the update. Real Redis decodes null to a `cjson.null` sentinel and
  may behave differently, so this is recorded rather than asserted until it is
  confirmed through integration mode.
