# Replay Consistency

Replay consistency tests verify that the same session, memory, summary, and track operations produce equivalent persisted results across backends. The current lightweight matrix only covers `InMemory` and `SQLite`, so it does not require external services and is suitable for local development and PR checks.

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
- `events`: messages, tool calls, tool responses, branch, filter key, tag, state delta, extensions, and actions
- `state`: visible merged session/app/user/temp state, normalized as tagged byte values so nil, JSON, UTF-8 text, and binary bytes remain distinct
- `memory`: content, topics, and metadata; raw memory IDs are only used for report context
- `summary`: `Session.Summaries[filterKey]`, summary text, topics, boundary metadata, and `GetSessionSummaryText`
- `tracks`: track name, each embedded event track, event order, payload, and timestamp

Generated fields such as event IDs, response IDs, timestamps, and backend-generated memory IDs are normalized. JSON normalization uses `json.Decoder.UseNumber` so large integers remain precise. Business-field differences are not allowed by default.

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

- snapshot mutation: partial event loss, summary loss, wrong session attribution, wrong summary filter key, large JSON-number drift, state byte representation drift, track payload drift, embedded track drift, and track order drift
- SQLite/public API injection: duplicate event, state pollution, memory pollution, and summary overwrite
- SQLite/storage injection: a duplicate memory row that simulates a backend retry bug or duplicate retry effect and verifies that it is reported as an unallowed memory diff

Injected anomalies must produce unallowed diffs by default. The normal replay matrix must have zero false positives.

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
- `path` is required and cannot be empty or a pure wildcard such as `*`, `**`, or `***`
- `backend_a` and `backend_b` are required and cannot be empty or `*`
- `reason` is required and cannot be blank
- backend pairs match in either order
- `path` supports partial globs such as `$.memory[*].content`

ID and timestamp differences should be fixed through normalization or runner changes, not allowed with `allowed_diff`.

## Extending Backends

The current runnable matrix only includes `InMemory` and `SQLite`. External backends such as Redis, PostgreSQL, MySQL, and ClickHouse are deferred and unsupported in the lightweight matrix. Future integrations should use an env-gated backend factory so default tests do not depend on external services.

When adding a backend:

- keep default local tests free of external-service dependencies
- normalize generated ID and timestamp fields
- preserve summary and track semantics across backends
- prove new backend differences are precisely locatable through anomaly tests before considering `allowed_diff`
