# Design

Replay consistency is a semantic contract, not byte-for-byte database equality.
Typed cases drive isolated Session and Memory services, then snapshots compare
events, scoped state, memories, filter-key summaries, and named tracks.

Normalization removes only non-portable values: physical IDs become logical IDs,
generated timestamps become presence markers, maps are canonicalized, and stored
memories are content-sorted. Ranked searches retain result order and scores.
State values use tagged forms so nil, JSON null, empty bytes, invalid JSON, and
arbitrary bytes remain distinct. Summary comparison includes text, filter key,
boundary, and retained event IDs. Retention is a boundary contract, not a claim
that the portable Session API physically deletes history.

Write recovery is opt-in. A domain witness checks the requested event, state,
memory, summary, or track effect after an error. Only idempotent State and
Memory writes may retry; other uncertain outcomes return `ErrUncertainCommit`.
Explicit reloads re-fetch and validate Session identity before later writes.

Concurrent event branches preserve lane order and predecessor relationships while
ignoring scheduler interleaving. State, Memory, Summary, and Track writes require
separate capabilities and disjoint footprints. Unsupported operations are
reported explicitly rather than inferred from backend behavior.

Diffs use JSON Pointer paths and domain locators. Each `AllowedDiff` names an
unordered backend pair, path glob, known rule, and reason. Reference mode uses
one oracle; consensus reports an outlier only when all remaining backends agree.

InMemory and file-backed SQLite form the lightweight matrix. External Redis,
PostgreSQL, MySQL, ClickHouse, vector, and IM adapters remain separately owned
integration work. `IMPLEMENTED` covers this package, `LOCAL_VERIFIED` covers
commands run against the lightweight matrix, `EXTERNAL_REQUIRED` covers live
services and fault injection, and `DESIGNED` covers contracts not yet exercised.
