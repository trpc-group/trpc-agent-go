# Design

Replay consistency is a semantic contract, not byte-for-byte database
equality. Typed cases drive `session.Service` and `memory.Service`; each
backend is isolated, replayed, read back, and closed. Snapshots cover events,
scoped state, memories, filter-key summaries, and named tracks.

Normalization removes only non-portable values. Physical IDs become logical
IDs, generated timestamps become presence markers, and maps are canonicalized.
Stored memories are content-sorted; ranked searches preserve order and score.
Event, response, and track times remain session-relative; caller-supplied
memory times remain UTC instants. State values use tagged `nil`, `json`, or `bytes` forms,
keeping nil, JSON null, empty bytes, and arbitrary bytes distinct. Event
content, tool data, extensions, state delta, memory metadata and ownership,
track payloads, and session identity remain comparable.

Summary comparison includes text, topics, filter key, update presence,
boundary version, cutoff, last event, and retained tail. An anchored boundary
must reference an observed event and match its timestamp. A probe session
detects cross-session summary leakage.

Concurrent replay is limited to event-only branches with stable internal
lanes. Global scheduler interleaving is ignored, but branch-local order,
predecessor relationships, and the complete event set must match. State,
memory, summary, and track writes remain sequential because their interfaces
define no portable cross-backend atomicity contract.

The comparator emits JSON Pointer paths with domain locators. `AllowedDiff`
requires an unordered backend pair, path glob, known rule, and reason; accepted
mismatches remain visible in the report. Reference mode uses one named oracle.
Consensus mode compares every successful pair and names an outlier only when
all remaining backends agree. Split results stay ambiguous, while failures and
unsupported capabilities remain separate evidence.

Adapters declare capabilities, so unsupported operations never disappear. The
root package supplies InMemory; a source-tree SQLite module binds real
file-backed Session and Memory services without imposing CGO on the root.
Further adapters reuse the same cases, normalization, and comparator.
