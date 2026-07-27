# Replay Consistency

Replay consistency tests verify that the same session, memory, summary, and track operations produce equivalent persisted results across backends. The current lightweight matrix only covers `InMemory` and `SQLite`, so it does not require external services and is suitable for local development and PR checks.

Reusable case, runner, snapshot, normalization, comparison, and report-encoding logic lives in `session/replaytest`. `test/replay_consistency_test.go` retains only InMemory/SQLite wiring, concrete cases, fault injection, and assertions, so another backend can reuse the same execution and comparison logic without copying the e2e implementation.

## Running

Run the targeted tests from the repository root:

```bash
cd test
CGO_ENABLED=1 go test ./... -run ReplayConsistency -count=1
```

You can also run the whole e2e module:

```bash
cd test
CGO_ENABLED=1 go test ./... -count=1
```

The SQLite backend uses `github.com/mattn/go-sqlite3`, so CGO and a C compiler are required.

## Report

The default report path is the repository root:

```text
session_memory_summary_track_diff_report.json
```

Override it with:

```bash
CGO_ENABLED=1 TRPC_AGENT_REPLAY_REPORT_PATH=replay-report.json go test ./... -run ReplayConsistency -count=1
```

A healthy matrix should write:

```json
[]
```

Each diff report entry contains:

```json
{
  "case": "case_name",
  "session_id": "session-case_name",
  "backend_a": "in_memory",
  "backend_b": "sqlite",
  "section": "summary",
  "path": "$.summary[\"root/tools/weather\"].summary",
  "left": "left value",
  "right": "right value",
  "allowed": false,
  "reason": "",
  "context": {
    "summary_filter_key": "root/tools/weather"
  }
}
```

The `context` object carries section-specific location data such as `event_index`, `summary_filter_key`, `memory_key`, `left_memory_id`, `right_memory_id`, `track_name`, and `track_event_index`.

## Compared Data

Snapshots include these sections:

- `session`: session ID, app, and user ID
- `events`: messages, tool calls, tool responses, caller-supplied event timestamp, branch, filter key, tag, state delta, extensions, and actions
- `state`: visible merged session/app/user/temp state, normalized as tagged byte values so nil, JSON, UTF-8 text, and binary bytes remain distinct
- `memory`: content, topics, and metadata; raw memory IDs are only used for report context
- `summary`: `Session.Summaries[filterKey]`, summary text, topics, boundary metadata, and `GetSessionSummaryText`
- `tracks`: track name, each embedded event track, event order, payload, and timestamp

Backend-regenerated event IDs, response IDs and timing metadata, and backend-generated memory IDs are omitted during normalization. Caller-supplied `Event.Timestamp` values are retained as UTC `RFC3339Nano` strings so persistence drift remains visible. JSON normalization uses `json.Decoder.UseNumber` so large integers remain precise. Business-field differences are not allowed by default.

Each memory query declares `ExpectedContents`. Search results are compared as an exact unordered content multiset, so backend-specific IDs, scores, and ranking are ignored while missing, unrelated, extra, and duplicate results remain observable.

Memory operation aliases follow canonical memory identity rather than content alone. Add aliases include app, user, content, kind, event time, participants, and location while intentionally excluding topics. An update always advances its referenced alias to the effective ID returned by the backend, so a later update or delete does not reuse an ID that was rotated after content or identity metadata changed.

Cases with app, user, or session state also validate each scope as a backend contract. The runner reads app and user state separately, creates a temporary peer session under the same app/user, and requires the peer to inherit only app/user values. The peer is deleted on every return path. Missing propagation, leaked session/temp state, and cleanup failures are runner errors; they are not snapshot differences and cannot be accepted with `allowed_diff`.

## Summary And Track Strategy

The Go version uses native session summary semantics. It does not create Python-style summary events and does not compare historical summary events.

Summary comparison covers:

- full summary: `session.SummaryFilterKeyAllContents`
- filter-key summaries such as `root/tools/weather`
- summary overwrite/update
- `SummaryBoundary` version, filter key, cutoff, and normalized last-event anchor
- `GetSessionSummaryText` results

A non-empty summary boundary anchor that cannot be mapped to the current snapshot events is reported as `last_event_index: -1`.

Track comparison covers:

- track name
- each `TrackEvent.Track` value
- event order within each track
- canonical JSON payload
- fixed timestamp

Note that `AppendTrackEvent` maintains `state["tracks"]`. When debugging track diffs, also check the track index in the state section.

## Anomaly Detection

The test harness includes three kinds of anomaly injection:

- snapshot mutation: partial event loss, event timestamp drift, summary loss, wrong session attribution, wrong summary filter key, large JSON-number drift, state byte representation drift, track payload drift, embedded track drift, and track order drift
- in-execution retry: fail-before-write must converge to the single-success baseline when retried with identical input; ambiguous fail-after-write verifies idempotent Memory Add, state update, and summary overwrite results
- SQLite/public API injection: state pollution, memory pollution, and summary overwrite
- SQLite/storage injection: a duplicate memory row that simulates storage corruption and verifies that it is reported as an unallowed memory diff

Injected anomalies must produce unallowed diffs by default. The normal replay matrix must have zero false positives.

`AppendEvent` currently does not deduplicate by event ID. The retry test therefore models a successful first write whose response fails, retries the identical event, and requires the shifted event at index 1 and the extra tail event at index 2 to be reported as unallowed diffs. This validates harness diagnostics without changing runtime Session idempotency semantics.

## allowed_diff

`allowed_diff` is only for explicitly recorded known acceptable differences. Business-field differences are not allowed by default.

Example:

```json
{
  "section": "memory",
  "path": "$.memory[*].content",
  "backend_a": "in_memory",
  "backend_b": "sqlite",
  "reason": "known backend-specific normalization gap"
}
```

Rules:

- `section` is required and cannot be empty or `*`
- `path` is required and must contain a concrete field or fixed index below the JSONPath root; root-only patterns such as `$`, `$*`, `$.*`, and `$[*]` are rejected together with pure wildcards such as `*`, `**`, and `***`
- `backend_a` and `backend_b` are required and cannot be empty or `*`
- `reason` is required and cannot be blank
- backend pairs match in either order
- `path` supports partial globs such as `$.memory[*].content`

ID and timestamp differences should be fixed through normalization or runner changes, not allowed with `allowed_diff`.

## Extending Backends

The current runnable matrix only includes `InMemory` and `SQLite`. External backends such as Redis, PostgreSQL, MySQL, and ClickHouse are deferred and unsupported in the lightweight matrix. Future integrations should use an env-gated backend factory so default tests do not depend on external services.

When adding a backend:

- keep default local tests free of external-service dependencies
- normalize backend-generated IDs and response timing metadata while preserving caller-supplied event timestamps
- preserve summary and track semantics across backends
- prove new backend differences are precisely locatable through anomaly tests before considering `allowed_diff`
