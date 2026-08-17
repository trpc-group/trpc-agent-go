# Agent Loop benchmarks

This directory defines the reproducible benchmark suites and allocation
budgets for Agent Loop hot paths. The benchmarks cover request construction,
summary projection and forks, context compaction, streaming, callbacks,
parallel tool-call invocation views (including session/state size crossed with
fan-out), telemetry, and event persistence.

## Suites

- `all` discovers every Go package containing a `Benchmark*` function in every
  supported module. Pull requests run it once with `-benchtime=1x` so stale or
  broken benchmarks cannot remain unnoticed.
- `agent-loop-core` contains small, deterministic allocation regressions. Its
  `B/op` and `allocs/op` values are checked against
  [`budgets.json`](budgets.json).
- `agent-loop` includes the core suite plus broader path benchmarks. Pull
  requests compare the base and head revisions with `benchstat`; main-branch
  runs after changes land also collect profiles.

The suites intentionally use selected short and typical scenarios rather than
the Cartesian product of every dimension. Expensive stress cases belong in
post-merge or manually dispatched workflows.

## Running benchmarks

Run the complete smoke suite:

```bash
.github/scripts/run-go-benchmarks.sh \
  --suite all \
  --mode smoke \
  --output benchmark-smoke.txt
```

Measure Agent Loop paths:

```bash
.github/scripts/run-go-benchmarks.sh \
  --suite agent-loop \
  --mode measure \
  --count 6 \
  --benchtime 200ms \
  --output benchmark-agent-loop.txt
```

Check the stable allocation budget:

```bash
.github/scripts/run-go-benchmarks.sh \
  --suite agent-loop-core \
  --mode measure \
  --count 3 \
  --benchtime 200ms \
  --output benchmark-core.txt
go run ./.github/scripts/benchguard \
  -input benchmark-core.txt \
  -budgets benchmarks/agentloop/budgets.json
```

## Adding a benchmark

Each benchmark must use deterministic fixtures, call `ReportAllocs`, and move
setup outside the timed section. Sub-benchmark names should use stable
`key=value` dimensions such as `history=256/chunks=64`. The operation measured
by one iteration must remain consistent across cases.

Every new benchmark is discovered automatically by the `all` smoke suite.
Add Agent Loop paths to [`suites.txt`](suites.txt) when they should also appear
in base/head reports. Add a hard allocation budget only for deterministic,
low-level paths whose allocation contract is understood and stable.

Time measurements from shared CI runners are informational. Hard pull-request
gates use allocation bytes and counts; post-merge profiles provide evidence
for investigating CPU or memory trends.

The base side of a pull-request comparison uses `--keep-going` because an older
revision can contain a benchmark that no longer compiles or runs. Failures are
kept in the artifact and reported by the tolerated base step while remaining
runnable benchmarks are still compared. Head, smoke, guard, and main-branch
runs never use this option.
