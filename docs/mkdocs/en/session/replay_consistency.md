# Replay Consistency

Replay consistency tests verify that the same session, memory, summary, and track operations produce equivalent persisted results across backends. The current lightweight matrix only covers `InMemory` and `SQLite`, so it does not require external services and is suitable for local development and PR checks.

Reusable case, runner, snapshot, normalization, comparison, and report-encoding logic lives in `session/replaytest`. `test/replay_consistency_test.go` retains only InMemory/SQLite wiring, concrete cases, fault injection, and assertions, so another backend can reuse the same execution and comparison logic without copying the e2e implementation.

`replaytest.Run` requires a non-empty run namespace with no surrounding whitespace. All backends in one logical comparison must receive the same namespace, while every rerun of the same case must use a new namespace. The namespace and case name are embedded in the app, user, and session identities, isolating session state, memory, summaries, and tracks on persistent services. Before creating a session, `Run` preflights statically detectable fixture errors in the fixed order track, event, summary, direct state-map prefix, sequential memory, then concurrent memory. Invalid track configuration, nil or non-normalizable events, invalid summary prefixes, disallowed direct state prefixes, unknown or unresolved sequential memory operations, and non-add concurrent memory operations therefore produce no backend calls or persisted session. Backend-dependent failures after that boundary may still leave partial replay data in place; callers that need cleanup own that lifecycle.

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
  "session_id": "session-7-run_123-case_name",
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

When a map key or list index exists on only one side, the missing side remains
`null` and the report adds `left_missing: true` or `right_missing: true` at the
diff-entry level. An omitted missing flag means that side is present, even when
its value is JSON `null`. For example, a missing left value compared with the
valid user value `{"replay":"missing"}` is encoded as:

```json
{
  "left": null,
  "right": {"replay": "missing"},
  "left_missing": true
}
```

The library never marks both sides missing. These flags do not apply to a nil
snapshot section: section-level nil remains a present `null` value and still
differs from an empty map or list without a missing flag. Older reports remain
valid JSON, but the ambiguity of their former `{"replay":"missing"}` sentinel
cannot be reconstructed after the fact.

The `context` object carries section-specific location data such as `event_index`, `summary_filter_key`, `memory_key`, `left_memory_id`, `right_memory_id`, `track_name`, and `track_event_index`.

## Compared Data

Snapshots include these sections:

- `session`: session ID, app, and user ID
- `events`: messages, tool calls, tool responses, caller-supplied event timestamp, branch, filter key, tag, state delta, extensions, and actions
- `state`: visible merged session/app/user/temp state, normalized as tagged byte values so nil, JSON, UTF-8 text, and binary bytes remain distinct
- `memory`: content, topics, and metadata; raw memory IDs are only used for report context
- `summary`: `Session.Summaries[filterKey]`, summary text, topics, boundary metadata, and `GetSessionSummaryText`
- `tracks`: the track map key, outer `TrackEvents.Track`, each embedded `TrackEvent.Track`, event order, payload, and timestamp

Backend-regenerated event IDs, response IDs and timing metadata, and backend-generated memory IDs are omitted during normalization. Caller-supplied `Event.Timestamp` values are retained as UTC `RFC3339Nano` strings so persistence drift remains visible. JSON normalization uses `json.Decoder.UseNumber` so large integers remain precise. Business-field differences are not allowed by default.

`Compare` and `CompareSnapshots` return an error instead of panicking when a caller-constructed snapshot contains a channel, function, NaN, or another value that cannot be converted to the canonical JSON comparison representation. Sections are converted in snapshot order, left before right, and comparison stops at the first error. A failed comparison returns no partial diffs and cannot be accepted by an `allowed_diff` rule.

Track payload fixtures accept the complete JSON value domain: objects, arrays, strings, numbers, booleans, and null. Persisted `json.RawMessage` values use a tagged snapshot with `nil`, `empty`, `json`, `utf8`, or `base64` kind. Valid JSON is canonicalized inside `payload.value`, while raw nil, empty bytes, JSON null, invalid UTF-8 text, and binary bytes remain distinguishable.

Each memory query declares `ExpectedContents`. Search results are compared as an exact unordered content multiset, so backend-specific IDs, scores, and ranking are ignored while missing, unrelated, extra, and duplicate results remain observable.

Memory operation aliases follow canonical memory identity rather than content alone. Add aliases include app, user, content, kind, event time, participants, and location while intentionally excluding topics. An update always advances its referenced alias to the effective ID returned by the backend, so a later update or delete does not reuse an ID that was rotated after content or identity metadata changed.

Fixture event preflight and final snapshot construction share the same marshal-and-decode normalization. Preflight rejects malformed caller events before persistence, while final snapshot construction retains the same validation to detect events corrupted by a backend or fault injection after execution. Snapshot construction also returns an error when a memory entry is nil, an entry has a nil `Memory` payload, a summary map entry has a nil value, or a track map entry has a nil `TrackEvents` container. These conditions are never discarded during normalization and cannot be accepted with `allowed_diff`. A non-nil empty track container remains valid. `BuildSnapshot` also validates and normalizes supplied memories when the session is nil, so the empty-session form cannot hide valid or malformed memory data.

Cases with direct `Case.AppState`, `Case.UserState`, or `Case.SessionState` updates also validate each independently defined scope as a backend contract. Before persistence, fixture preflight permits unprefixed and `user:` keys in `UserState` while rejecting `app:` and `temp:`, and permits unprefixed and `temp:` keys in `SessionState` while rejecting `app:` and `user:`. The `UserState` restriction is a harness-level policy for the supported matrix, not a general `session.Service` contract: InMemory rejects these cross-scope prefixes while SQLite currently accepts them, so preflight rejects them uniformly. An invalid direct state fixture returns before any backend call. `AppState` and `InitialState` retain their existing key semantics. The runner compares the direct app/user updates with `ListAppStates` / `ListUserStates`, then creates a temporary peer session under the same app/user and requires it to inherit only those app/user values. Peer deletion is attempted after every creation attempt, including ambiguous fail-after-write errors, using a bounded context detached from caller cancellation. Missing propagation, leaked session/temp state, and cleanup failures are runner errors; they are not snapshot differences and cannot be accepted with `allowed_diff`.

`Event.StateDelta` follows the routing semantics of `SessionService.AppendEvent`. The replay runner does not infer an independent app/user-store contract merely from an `app:` or `user:` key prefix. In the current supported InMemory/SQLite matrix, prefixed event deltas remain session-local; real matrix coverage fixes that behavior by exercising multiple prefixed deltas and overwrite order, checking that the independent app/user stores remain unchanged, and comparing the resulting snapshots. MongoDB is not part of this matrix or this contract. If a future supported backend needs different routing semantics, that expectation must first be introduced as an explicit case/backend policy rather than being inferred implicitly by the generic runner.

## Summary And Track Strategy

The Go version uses native session summary semantics. It does not create Python-style summary events and does not compare historical summary events.

Each `SummaryStep` may set `EventPrefix` to the number of leading case events that must be appended before that summary runs. Before persistence, the runner resolves the complete prefix sequence: prefixes must stay within the event list and be monotonically non-decreasing, equal prefixes are allowed, and nil means all events. Timeline execution only consumes these validated targets. This allows a case to append events, summarize, append more events, and verify that the stored boundary advances without discovering a later fixture error after earlier steps have already been stored.

`Backend.CreateSummary`, when provided, owns the complete per-step operation: fixture-specific summary preparation and summary persistence. The callback must be safe for concurrent `Run` calls that share a backend. When it is nil, the runner calls `SessionService.CreateSessionSummary` directly.

Summary comparison covers:

- full summary: `session.SummaryFilterKeyAllContents`
- filter-key summaries such as `root/tools/weather`
- summary overwrite/update
- `SummaryBoundary` version, filter key, cutoff, and normalized last-event anchor
- `GetSessionSummaryText` results

A non-empty summary boundary anchor that cannot be mapped to the current snapshot events is reported as `last_event_index: -1`.

Track comparison covers:

- track map-key name
- outer `TrackEvents.Track` container identity
- each `TrackEvent.Track` value
- event order within each track
- tagged payload representation, with canonical JSON under `payload.value`
- fixed timestamp

Note that `AppendTrackEvent` maintains `state["tracks"]`. When debugging track diffs, also check the track index in the state section.

## Anomaly Detection

The test harness includes five kinds of anomaly injection:

- snapshot mutation: partial event loss, event timestamp drift, summary loss, wrong session attribution, wrong summary filter key, large JSON-number drift, state byte representation drift, track payload drift, embedded track drift, outer track-container drift, and track order drift
- service-contract mutation: a stale summary boundary after interleaved event appends, an incorrect outer track identity, and JSON null restored as a nil raw payload
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
- `path` is required, must start at the declared section root, and must contain a concrete field, quoted key, or fixed index below that root
- global-root patterns such as `$`, `$*`, `$.*`, and `$[*]`, pure wildcards such as `*`, `**`, and `***`, and section-root patterns such as `$.memory`, `$.memory*`, `$.memory.*`, and `$.memory[*]` are rejected
- a quoted key contributes specificity only when its decoded value contains a non-`*` literal; quoted `"*"`, `"**"`, and escaped equivalents such as `"\u002a"` are pure wildcards and do not make a rule concrete
- `backend_a` and `backend_b` are required and cannot be empty or `*`
- `reason` is required and cannot be blank
- backend pairs match in either order
- `path` supports partial globs such as `$.memory[*].content`

ID and timestamp differences should be fixed through normalization or runner changes, not allowed with `allowed_diff`.

## Extending Backends

The current runnable matrix only includes `InMemory` and `SQLite`. External backends such as Redis, PostgreSQL, MySQL, and ClickHouse are deferred and unsupported in the lightweight matrix. Future integrations should use an env-gated backend factory so default tests do not depend on external services.

When adding a backend:

- keep default local tests free of external-service dependencies
- give the backend a non-empty name with no surrounding whitespace; the name is the report and `allowed_diff` identity
- provide `Backend.ReadAllMemories`; alias resolution and final snapshots share this callback, and it must return `complete=true` only after backend-specific pagination, total-count validation, or a documented unbounded read proves that every memory was returned
- normalize backend-generated IDs and response timing metadata while preserving caller-supplied event timestamps
- preserve summary and track semantics across backends
- prove new backend differences are precisely locatable through anomaly tests before considering `allowed_diff`
