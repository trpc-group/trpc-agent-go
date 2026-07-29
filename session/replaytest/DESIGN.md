# Replay Consistency Design

The replay framework separates scenario metadata, backend operations, capture,
normalization, and comparison. `PublicCases` owns the stable case names,
capability requirements, and ordering rules. A backend matrix supplies the
operations so the same scenarios can exercise in-memory, SQLite, Redis, or a
custom store without importing the repository's end-to-end test package.

Each case writes deterministic events, state, memory, summaries, or tracks.
`Capture` reads the session, memory, app-state, and user-state view, then
`Normalizer` removes volatile timestamps and maps backend-generated
identifiers, including request IDs, to snapshot-scoped aliases. Empty
identifiers and equality relationships remain observable, and repeated event
or memory IDs surface under `duplicate_ids`. `Compare` reports exact JSON
paths and permits differences only through an `AllowedDiff` rule bound to the
case, both backends, section, path, and reason. A section skipped for a
one-sided unsupported capability still yields a visible allowed diff whenever
either side holds content, and an `inconclusive` report stays a separate
outcome from unexpected diffs.

The injected anomaly matrix proves the four required summary failures:
`summary loss` detects a missing summary, `summary overwrite` detects incorrect
replacement content, `summary ownership` detects assignment to the wrong
session, and filter ownership is covered by two test variants, `summary filter
key` and `summary boundary filter key`. Other cases cover event loss or duplication,
state changes, memory ordering and identity, track payloads, concurrency, and
lost-acknowledgement recovery.

The official lightweight entry point is:

```bash
cd test && CGO_ENABLED=1 go test -run Replay -count=1
```
