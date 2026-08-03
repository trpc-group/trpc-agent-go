# Design

Replay consistency is a semantic contract, not byte-for-byte database
equality. Typed cases drive isolated `session.Service` and `memory.Service`
instances, then snapshots compare events, scoped state, memories, filter-key
summaries, and named tracks.

Normalization removes only values that are not portable. Physical IDs become
logical IDs, generated timestamps become presence markers, maps are
canonicalized, and stored memories are content-sorted. Ranked searches retain
their order and score. State values use tagged `nil`, `json`, or `bytes`
representations, so nil, JSON null, empty bytes, invalid JSON, and lossy JSON
objects remain distinct. Summary comparison includes text, filter key,
boundary, retained events, and ownership. Track payloads and session-relative
timing remain observable.

Write recovery is opt-in. After an error, a domain witness checks logical event
count, state postconditions, semantic memory identity, summary change, or track
tuple count. Only State and Memory may retry because their writes are
idempotent. Other unproven outcomes return `ErrUncertainCommit` rather than
risk a duplicate append.

Concurrent event branches preserve lane order and predecessor relationships
while ignoring scheduler interleaving. Each concurrent step uses one write
domain. State, Memory, Summary, and Track writes require separate backend
capabilities and disjoint footprints. Full-session summaries, event state
deltas, reads, reloads, and nested concurrency stay sequential because the
service contracts do not define portable conflict resolution for them.

Diffs use JSON Pointer paths plus session, event, memory, summary, or track
locators. An `AllowedDiff` needs a backend pair, path glob, known rule, and
reason, and remains visible in the report. Reference mode uses one oracle;
consensus mode identifies an outlier only when every remaining backend agrees.
Failures and unsupported capabilities remain explicit evidence.

InMemory and file-backed SQLite form the lightweight matrix. Optional external
adapters belong in the independent integration module and declare only the
capabilities they actually wire.
