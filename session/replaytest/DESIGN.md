# Design

Replay consistency is modeled as a semantic contract, not byte-for-byte
database equality. Cases use typed operations over `session.Service` and
`memory.Service`. Each backend is isolated, replayed, read back, and closed.
Snapshots separate globally or causally ordered events, scoped state,
memories, filter-key summaries, and named tracks.

Normalization removes only values that do not carry portable semantics.
Physical IDs become logical IDs, generated timestamps become presence markers,
maps are canonicalized, and memories are sorted by normalized content.
User-supplied memory times remain UTC instants. Track durations and timestamps
are volatile, while status and errors remain semantic. Event content, tool
data, extensions, state delta, summary text, boundary, filter key, retained
tail, and ownership remain comparable. Concurrent cases ignore global
interleaving but preserve branch-local order and the complete event set.

The comparator emits JSON Pointer paths with session, event, memory, summary,
or track locators. `AllowedDiff` requires an unordered backend pair, path glob,
known rule, and reason; malformed rules fail before replay.

Reference mode uses one named oracle. Consensus mode compares every successful
pair deterministically and names an outlier only when all remaining backends
agree. Split votes, two-backend disagreements, and non-transitive results stay
ambiguous. Allowed differences count as agreement; execution failures and
unsupported capabilities remain separate evidence.

Adapters declare capabilities, so unsupported operations never disappear.
The root package supplies InMemory; SQLite uses a nested module. Other adapters
register without changing cases, normalization, or comparison code.
