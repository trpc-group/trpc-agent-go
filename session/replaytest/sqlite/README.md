# SQLite replay integration test

This source-tree test module binds `session/replaytest` to the file-backed
Session and Memory SQLite services. It intentionally exposes no importable
adapter: a separately published adapter cannot depend on the new root package
until that package has been released. Each case uses two temporary database
files and synchronous session persistence with the same deterministic
summarizer and filter-key policy as the InMemory reference.

SQLite requires CGO and a C compiler:

```bash
CGO_ENABLED=1 go test ./... -count=1
```

The integration test runs all public cases with a 30-second deadline. Exceeding
that deadline fails the test, enforcing the issue's lightweight-mode acceptance
limit while also propagating cancellation to cooperative backends.

`TestSQLitePersistenceFaultsReachReport` adds eight persistence-level fault
classes by mutating rows in the actual SQLite Session or Memory database after
the service write: event content, Session state, Memory content, Track payload,
missing Summary, stale Summary content, Summary filter key, and Summary moved
to another Session. The stale-summary case restores the previous summary row
after the real update; it does not simulate a dropped service call. Each
scenario continues through replay, normalization, comparison, `Report.Validate`,
`WriteReport`, JSON decoding, and a second `Report.Validate`, and verifies that
the temporary database root is empty after cleanup. This is evidence for those
eight classes only; the 21-case package fault matrix remains normalized
Snapshot/Compare mutation coverage.
