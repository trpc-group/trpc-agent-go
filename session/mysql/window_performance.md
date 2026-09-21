# MySQL event-window regression check

Run against a disposable MySQL database using the repository's default schema:

```sh
cd session/mysql
export TRPC_AGENT_GO_MYSQL_TEST_DSN='root@tcp(127.0.0.1:13326)/pr2606?parseTime=true'
go test -run 'TestService_GetEventWindow.*MySQLIntegration' -count=1 .
GOMAXPROCS=2 go test -run '^$' -bench '^BenchmarkService_GetEventWindowMySQL$' -benchtime=500ms -count=3 .
```

The benchmark creates uniquely prefixed tables and drops only those tables on
completion. It skips without the DSN. Fixture preparation is outside timing;
every iteration checks the complete returned event-ID sequence. `queries/op`
counts client Query calls, not network round trips. To compare an earlier
revision, copy this benchmark file into its `session/mysql` module as well.

## Query strategy

The default lookup index orders by `(app_name,user_id,session_id,created_at)`.
Adding `id` to the SQL ordering can turn a small LIMIT into a sort of the entire
remaining range. An index migration is not required by this change:

- Read a time-ordered prefix of 65 rows, using the same index as the original
  time-only query. Sort complete timestamp groups by row ID in Go.
- Defer the last timestamp group when the prefix may have cut it in half. A
  shorter returned batch is not necessarily the end of the window.
- If all 65 rows have one timestamp, select metadata from only that timestamp
  ordered by ID, then fetch its payloads. JSON does not enter the tie-breaker sort.
- Find an anchor's first matching timestamp with a time-only LIMIT, choose its
  lowest matching row ID at that timestamp, then load JSON by primary key in the
  same SQL call. This preserves deterministic duplicate-ID behavior without
  sorting all later events or transferring JSON through a sort.

All selects retain `user_id` for shard routing, including each anchor subquery
and the primary-key payload lookups. No tuple predicate is used. Real TDSQL
execution is not covered by the local measurements below.

This is not a universal 65-row bound on physical reads: deleted rows, filtering
and a very large equal-timestamp group can require more work. The fallback bounds
the sort to one timestamp, not to 64 examined rows. It can use extra queries for
ties. The benchmark below uses distinct timestamps and therefore measures the
common indexed path; the integration tests separately cover large timestamp
groups, role filtering, missing payloads and duplicate anchors.

## Local comparison (2026-09-22)

MySQL 8.4.11 in Docker on Windows amd64, Ryzen 7 7840HS, Go 1.27.1,
`GOMAXPROCS=2`, 512 MiB InnoDB buffer pool, 256 KiB sort buffer. One request at a
time and one retained SQL connection. Five sessions contain 49,280 events, with
strictly increasing microsecond timestamps. Three alternating base/fix runs,
five warmup requests per case, 500 ms per measured run. Values are medians of
the three per-run mean latencies, not production latency percentiles.

Base is `c982cfe042f5b8ada3264691618f4659c1429c22`. The original PR revision is
`4a49d1202619d762e96525e147cfe821b8d3f155`; its measurements came from the preceding
three-run reproduction on the same fixture, rather than the alternating fix run.

| Case | Base ms/op | Original PR ms/op | Fixed ms/op | Base / original / fixed queries |
|---|---:|---:|---:|---:|
| 128 small events | 5.208 | 8.383 | 5.566 | 4 / 7 / 4 |
| 4,096 small events | 8.744 | 21.821 | 8.752 | 4 / 7 / 4 |
| 4,096 large events (32 KiB content) | 88.891 | 105.545 | 90.956 | 4 / 7 / 4 |
| 32,768 small events | 32.640 | 104.729 | 32.108 | 4 / 7 / 4 |
| First anchor only | 1.304 | 8.999 | 1.577 | 2 / 3 / 2 |
| Middle anchor only | 4.645 | 9.204 | 4.755 | 2 / 3 / 2 |
| Sparse role (1/128 of 8,192 events) | 23.543 | 94.973 | 23.701 | 10 / 19 / 10 |

The main range-scan regression is removed in these fixtures. The first-anchor
case still has about 0.27 ms additional cost, and the short window about 0.36 ms;
this is not a claim that every workload is faster than the base.

For the 4,096-event before-side probe, EXPLAIN ANALYZE changes from an index scan
of 2,049 rows plus filesort to an index-ordered scan of 66 rows returning 65 (the
cursor row is filtered out). The original time-only base scans 64. The fixed
anchor discovers the first matching timestamp without scanning later timestamps.
Tests with 140 equal-time, 32 KiB JSON payloads and a 32 KiB session sort buffer
also pass, including a duplicated external anchor ID.

No cold-cache, concurrent-load, or real TDSQL results are implied.
