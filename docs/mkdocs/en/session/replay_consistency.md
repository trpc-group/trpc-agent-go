# Session Replay Consistency

The `session/replaytest` package replays one typed operation program against
multiple Session and Memory backends, captures transition checkpoints plus the
final state, normalizes backend noise, and emits field-level structured diffs.
The public matrix contains 12 cases covering dialogue, tools, state, memory,
summary overwrite/window recovery, tracks, concurrency, retry recovery, and
duplicate identity preservation.

## Lightweight mode

The default integration test compares:

- InMemory Session + InMemory Memory
- SQLite Session + SQLite Memory

It requires no API key or external service. SQLite requires CGO and a C
compiler.

```bash
cd test
CGO_ENABLED=1 go test . -run ReplayConsistency -count=1
```

The test also proves that every public case detects at least one injected
anomaly. Extra tests exercise public service wrappers and direct SQLite storage
corruption. The complete lightweight suite has a 30-second upper bound; normal
replays are expected to produce zero blocking diffs.

## Acceptance metrics

Run the metric-bearing tests with verbose output:

```bash
cd test
go test . -run 'TestReplayConsistency(Lightweight|DetectsInjectedFaults)$' -v -count=1
```

The tests enforce and print the following acceptance values:

| Metric | Required | Current matrix |
| --- | --- | --- |
| Public replay cases | At least 10 | 12 |
| Injected-fault detection | 100% and at least one detected fault per public case | 15/15 |
| Normal-case false positives | At most 5% | 0/12 |
| Required Summary fault detection | 100% | loss, overwrite, wrong session, and wrong filter-key |
| Complete lightweight duration | Less than 30 seconds | Enforced by the tests |

## Optional integration mode

Set any of the following variables before running the same command:

| Backend | Environment variable |
| --- | --- |
| Redis | `TRPC_AGENT_REPLAY_REDIS_URL` |
| PostgreSQL | `TRPC_AGENT_REPLAY_POSTGRES_DSN` |
| MySQL | `TRPC_AGENT_REPLAY_MYSQL_DSN` |
| ClickHouse | `TRPC_AGENT_REPLAY_CLICKHOUSE_DSN` |

Unset integrations are skipped explicitly. Each enabled backend receives a
unique table/key prefix. Optional Session backends currently use InMemory
Memory so Session capability differences can be isolated. ClickHouse declares
Track as unsupported/allowed because its Session service does not implement
`session.TrackService`.

## Design (213 words)

The harness separates operation execution, capture, normalization, comparison,
and reporting. A logical identity ledger maps backend-generated event,
invocation, tool-call, and memory identifiers to case-defined identities; this
avoids order-based aliasing and preserves equal-content duplicates. Event
timestamps, request IDs, JSON/map ordering, private timing metadata, and
configured duration fields are removed, while state bytes remain tagged as
nil, JSON, UTF-8, or base64. JSON decoding uses `UseNumber`, so large integers
are not rounded. Memory comparison preserves multiplicity, normalizes metadata,
supports exact or unordered retrieval profiles, and rounds scores only to the
declared precision.

Summary comparison includes owner app/user/session, map key and embedded
filter-key, text, version, overwrite checkpoints, update/cutoff event indexes,
and logical last-event identity. Track comparison preserves track name and
event sequence, recursively normalizes invocation/tool references, and removes
only explicitly volatile duration/latency keys. Concurrent cases first validate
exact multiplicity and happens-before edges, then canonicalize legal
cross-branch interleavings.

An `allowed_diff` must name both backends and one exact section/path with a
non-empty explanation; wildcards and broad suppression are rejected.
Capabilities are explicit, and unsupported behavior is retained in reports.
Factories create an isolated backend per case. The lightweight factory uses
InMemory and SQLite; Redis, PostgreSQL, MySQL, and ClickHouse factories are
environment-gated. Reports are written through one synchronized temp-file,
fsync, and rename path.

## Report

The checked-in example is:

```text
test/testdata/session_memory_summary_track_diff_report.json
```

Each diff records the case, session ID, backend pair, section/path, presence of
both values, values, checkpoint, and `allowed_diff` explanation. Applicable
diffs also include event index, memory ID, summary ID/filter-key, or track name.
