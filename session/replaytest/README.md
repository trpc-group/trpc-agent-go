# session/replaytest

Multi-backend Session / Memory / Summary / Track **replay consistency** harness
for issue #2001.

## One-command lightweight matrix

From the repo root (CI runs this package):

```bash
go test ./session/replaytest/ -count=1 -run TestReplayLightweightMatrix
```

Or the full package:

```bash
go test ./session/replaytest/ -count=1
```

From the `test/` e2e module (so `go test ./test -run Replay` finds tests):

```bash
cd test && go test . -count=1 -run Replay
```

Lightweight mode uses two independent **InMemory** backends over `AllCases()`
and is intended to finish within **30s** on a developer machine.

## SQLite (optional, CGO)

SQLite lives in a separate module so the root module does not force CGO:

```bash
cd session/replaytest/sqlite
CGO_ENABLED=1 go test . -count=1
```

## Integration backends

External backends can be registered directly with `NamedBackend` once their
adapter modules provide concrete `session.Service` / `memory.Service` values.
Vendor-specific env factories are intentionally not exposed from this generic
package until they have real success paths.

## Public cases

`AllCases()` is the public matrix (conversation, state, memory, summary, track,
concurrency, recovery, app/user state, summary filter-key isolation, memory
lifecycle, multi-session isolation).

Example report asset:

`testdata/session_memory_summary_track_diff_report.json`

## Design

See [DESIGN.md](./DESIGN.md).
