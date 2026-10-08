# Design

Replay consistency is a semantic contract, not byte equality. Typed cases drive
isolated Session and Memory services, then snapshots compare events, scoped
state, memories, filter-key summaries, and named tracks.

Normalization removes only non-portable values: physical IDs become logical IDs,
generated timestamps become presence markers, maps are canonicalized, and stored
memories are content-sorted. Ranked searches retain result order and scores.
State tags keep nil, JSON null, empty bytes, invalid JSON, and arbitrary bytes
distinct. Summary comparison includes text, filter key, boundary, and retained
event IDs. Retention is a boundary contract; it does not claim physical deletion.

Write recovery is opt-in. A domain witness checks the requested event, state,
memory, or track effect after an error. Summary `RecoveryVerify` is rejected
because backend-owned summarizers provide no portable expected text. Only
idempotent State and Memory writes may retry; other uncertain outcomes return
`ErrUncertainCommit`. Reloads validate Session identity before later writes.

Concurrent event branches preserve lane order and predecessor relationships while
ignoring scheduler interleaving. State, Memory, Summary, and Track writes require
separate capabilities and disjoint footprints. Unsupported operations are
reported explicitly rather than inferred from backend behavior.

Offset pages retain backend storage order; TTL cases prove visibility before and
disappearance after the adapter-reported deadline. Reference manifests enumerate
every comparable pair.

Diffs use JSON Pointer paths and domain locators. Each `AllowedDiff` names an
unordered backend pair, path glob, known rule, and reason. Reference mode uses
one oracle; consensus reports an outlier only when all remaining backends agree.
The runner and validator cap backends at 64 because consensus is O(n^2).

InMemory and file-backed SQLite form the lightweight matrix. External Redis,
PostgreSQL, MySQL, ClickHouse, vector, and IM adapters remain separate
integration work. `IMPLEMENTED` covers this package, `LOCAL_VERIFIED` the
lightweight matrix, `EXTERNAL_REQUIRED` live services, and `DESIGNED`
unexercised contracts.
