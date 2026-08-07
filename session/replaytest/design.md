# Replay Consistency Test Framework

## Design Overview

The replay consistency test framework (`session/replaytest/`) verifies that
different Session/Memory/Summary/Track backends produce identical results
when driven by the same sequence of operations. It is designed for the
tRPC-Agent-Go multi-backend architecture (InMemory, SQLite, Redis, Postgres,
MySQL, ClickHouse).

## Architecture

The framework has four layers:

1. **ReplayCase + ReplayOp** — a declarative, backend-agnostic operation
   sequence (CreateSession, AppendEvent, UpdateState, AddMemory, etc.).
2. **Harness** — orchestrates execution of all cases across all enabled
   backends, normalizes results, and feeds them to the comparator.
3. **Comparator + Normalizer** — field-by-field comparison with configurable
   tolerances (ID/timestamp/JSON order/map sort/float), producing `DiffEntry`
   records.
4. **Reporter** — aggregates diffs into a structured JSON report and a
   human-readable text summary.

## Key Design Decisions

- **Field-level normalization** before comparison eliminates auto-generated
  IDs, normalizes timestamps to UTC, sorts JSON keys and map traversal, and
  strips backend-private metadata.
- **AllowedDiff rules** cover known variances (event paging, TTL, vector
  search ordering, track support) so only genuine inconsistencies are flagged.
- **TrapInjector** deliberately mutates one backend's result to verify the
  comparator detects the injected difference.

## Scope

10 predefined replay cases cover single/multi-turn conversation, tool calls,
state, memory, summary, track events, concurrent writes, and idempotency.
The framework runs in ~50ms for InMemory+SQLite, requires no API keys, and
all external backends are opt-in via environment variables.