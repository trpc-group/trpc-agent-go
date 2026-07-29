# Replay Consistency Design

The replay framework separates scenario metadata, backend operations, capture,
normalization, and comparison. `PublicCases` owns the stable case names,
capability requirements, and ordering rules. A backend matrix supplies the
operations so the same scenarios can exercise in-memory, SQLite, Redis, or a
custom store without importing the repository's end-to-end test package.

Each case writes deterministic events, state, memory, summaries, or tracks.
`Capture` reads the resulting session and memory view, then `Normalizer`
removes volatile timestamps and maps backend-generated identifiers to
snapshot-scoped aliases. Empty identifiers and equality relationships remain
observable. `Compare` reports exact JSON paths and permits differences only
when a capability and a narrow `AllowedDiff` rule both document them.

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
