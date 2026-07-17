# Session Replay Consistency Harness

This harness replays the same normalized Session, Memory, Summary, and Track
operation stream through the real in-memory and SQLite Session/Memory services. It
ships ten cases covering single and multi-turn conversations, tool calls and
argument extensions, state overwrite/delete semantics, memory, summary update,
summary plus truncated events, tracks, concurrent ordering, and retry recovery.

From this directory:

This example is intended to run inside the trpc-agent-go repository. Its
`go.mod` uses relative `replace` directives for the root, Session SQLite, and
Memory SQLite modules; copying this directory outside the monorepo will break
module resolution unless those replacements are removed or adjusted.

```bash
# normal replay: expects no non-allowed differences
go run . --output report.json

# fault campaign: injects one mismatch into every public case
go run . --inject --output report.json

go test ./...
```

The lightweight run needs no external services. Both adapters execute
`CreateSession`, `AppendEvent`, `UpdateSessionState`, `AddMemory`,
`CreateSessionSummary`, `AppendTrackEvent`, and the corresponding reads. The
persistent adapter uses temporary SQLite databases so CI remains deterministic.

Every `ReplayCase` separates its expected snapshot from a `Run` callback that
drives backend operations. The concurrency case appends two branches behind a
barrier, retry recovery injects a failure after a stable-ID event commit and
then retries idempotently, and summary update writes two successive summaries
for the same filter key. Tool-call and tool-result IDs are round-tripped
explicitly, including out-of-order results. Memory reads use the service's
unlimited mode and are regression-tested with 101 entries.

The executable compares each backend with the expected snapshot in addition to
the backend-to-backend comparison. Fault injection counts a case only when the
mutation introduces a new non-allowed difference under that case's declared
target path; pre-existing or allowed capability differences cannot satisfy the
detection campaign.

Redis, Postgres, MySQL, and ClickHouse integration
runs should be enabled only when their project-specific environment variables
are configured, and unsupported pagination, TTL, Track, or query capabilities
must be added to `Snapshot.Unsupported` with a reason. Such capability gaps are
reported as documented `allowed_diff`; data loss is never allowed.

The report identifies the case, backend, session, collection locator, JSON
field path, normalized baseline and compared value, plus allowed-difference
status and explanation.
