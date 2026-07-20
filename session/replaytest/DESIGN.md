# Design

Replay consistency is modeled as a semantic contract, not byte-for-byte
database equality. A case contains typed operations that use the repository's
existing `session.Service` and `memory.Service` interfaces. Every backend is
opened in isolation for one case, replayed, read back, and closed before the
next case. The captured view separates session identity, globally or causally
ordered events, scoped state, memories, filter-key summaries, and named tracks.

Normalization removes only values that do not carry portable semantics.
Physical event and memory IDs become stable logical IDs; timestamps become
presence markers; JSON objects and maps are canonicalized; memory results are
sorted by normalized content; generated timestamps become presence markers,
while user-supplied memory event times remain UTC instants; duration, latency,
and timestamp fields inside track payloads become presence markers. Event role/content/tool data,
extensions, state delta, summary text and boundary version/filter key, retained
post-summary events, track status/error data, and summary ownership remain comparable.
Concurrent cases relax global interleaving but still compare each branch's
causal order and the full event set.

The comparator walks normalized JSON and emits JSON Pointer paths with the
nearest session ID, event index, memory ID, summary filter key, or track name.
An `AllowedDiff` must name both backends, a valid path glob, an `ignore`,
`same_type`, or `within_delta` rule, and a reason; malformed or undocumented rules fail before replay. Backend adapters
declare capabilities explicitly, so unsupported operations appear in the
report instead of disappearing through test skips. The root package supplies
InMemory; SQLite lives in a small nested module to keep CGO out of the root
dependency surface. Other persistent adapters can register without changing
cases, normalization, or comparison code.
