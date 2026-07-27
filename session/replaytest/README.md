# Session and Memory Replay Consistency

`replaytest` is a small, backend-neutral harness for verifying that the same
conversation replay produces the same persisted session and memory view. The
lightweight suite compares InMemory with SQLite and needs no service
credentials. Additional Redis, Postgres, MySQL, or ClickHouse backends can be
added by constructing a `replaytest.Backend` with their existing session and
memory services and passing it to `replaytest.Run`.

Each replay case uses a fixed app, user, session ID, event payload, and track
payload. The snapshot reader loads events, session state, filter-key summaries,
tracks, and memories after the mutation sequence. JSON is decoded before
comparison, so map ordering never produces a mismatch. The normalizer removes
generated event and response IDs plus wall-clock fields such as event,
summary, track, and memory timestamps. Memory IDs remain visible because they
identify the affected memory in a report. Those fields are the current
`allowed_diff` policy; a backend-specific field should be removed only when it
cannot affect replay semantics.

`StandardCases()` contains ten public cases: a single turn, multiple turns,
tool calls and argument extensions, state overwrite/deletion, memory writes,
summary update, summary plus later events, tracks, deterministic interleaving,
and retry recovery. `Run` uses the first backend as the baseline and emits one
field-level report record per mismatch. A record carries the case, backend,
session ID, field path, baseline JSON, actual JSON, `allowed_diff`, and an
explanation for replay execution failures. The checked-in JSON fixture is the
expected report from the lightweight run.

Run the suite with:

```bash
cd session/replaytest && go test ./...
```
