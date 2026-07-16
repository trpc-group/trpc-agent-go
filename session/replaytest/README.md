# replaytest

Multi-backend replay consistency harness for `trpc-agent-go` Session / Memory /
Summary / Track services (issue #2001).

## Quick start (InMemory)

```go
h := replaytest.NewHarness(replaytest.DefaultHarnessOpts())
sess, mem, profile, err := replaytest.InMemoryFactory()()
if err != nil { /* handle */ }
defer sess.Close()
defer mem.Close()
h.AddBackend(replaytest.NamedBackend{
    Name: "inmemory", Profile: profile,
    SessionService: sess, MemoryService: mem,
})
report, err := h.Run(context.Background(), replaytest.AllCases())
```

## SQLite dual-backend

```bash
cd session/replaytest/sqlite
go test .
```

The SQLite adapter is a separate module so the root module does not force CGO.

## Public cases

See `AllCases()` and `DESIGN.md`.

## Report

```go
_ = replaytest.WriteReportJSON(os.Stdout, report)
```

Diffs include `session_id`, `event_index`, `summary_filter_key`, `track_name`,
`memory_id`, and `path`.

## Fault injection

```go
_ = replaytest.InjectFault(snapshot, replaytest.FaultOverwriteSummary)
```
