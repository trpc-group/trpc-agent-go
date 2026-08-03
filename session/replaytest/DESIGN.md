# Design

Replay consistency is a semantic contract, not byte-for-byte database equality.
Typed cases drive isolated `session.Service` and `memory.Service` instances,
then snapshots compare events, scoped state, memories, filter-key summaries,
and named tracks.

Normalization removes only non-portable values. Physical IDs become logical
IDs, generated timestamps become presence markers, maps are canonicalized, and
stored memories are content-sorted. Ranked searches retain order and score.
Tagged State representations keep nil, JSON null, empty bytes, invalid JSON,
and lossy JSON objects distinct. Summary comparison includes text, filter key,
boundary, retained events, and ownership; Track payloads and session-relative
timing remain observable.

Write recovery is opt-in. After an error, a domain witness checks event count
and fingerprint, State postconditions, semantic Memory identity, Summary
change, or Track tuple count. Only idempotent State and Memory writes may retry.
Other unproven outcomes return `ErrUncertainCommit`. Explicit reloads re-fetch
the active Session, validate its app, user, and session IDs, and make subsequent
writes use that value.

Concurrent Event branches preserve lane order and predecessor relationships
while ignoring scheduler interleaving. State, Memory, Summary, and Track writes
require separate capabilities and disjoint footprints. Full-session summaries,
event State deltas, reads, reloads, and nested concurrency stay sequential
because their service contracts provide no portable conflict resolution.

Diffs use JSON Pointer paths plus domain locators. Every `AllowedDiff` names an
unordered backend pair, path glob, known rule, and reason, and remains visible
in the report. Reference mode uses one oracle; consensus reports an outlier only
when every remaining backend agrees. Failures and unsupported capabilities
remain explicit evidence.

InMemory and file-backed SQLite form the lightweight matrix. Optional external
adapters live in their owning integration modules and declare only capabilities
they actually wire.
