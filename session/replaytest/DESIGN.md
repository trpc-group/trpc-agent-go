# Replay Consistency Harness Design

The replay harness loads JSON cases and drives the same operations through each
enabled backend. InMemory is the baseline; SQLite is always included in
lightweight mode, while external backends are env-gated for integration runs.
Each backend is read back through public Session and Memory APIs into one
`Snapshot` containing events, state, memories, summaries, and track events.

Normalization removes backend-private noise before comparison. Memory IDs become
stable ordinals, timestamps are zeroed, memory scores are rounded, maps and JSON
payloads are canonicalized, and framework-managed state keys are dropped. The
comparator then performs field-level comparison and emits precise locators such
as event index, summary filter-key, memory ID, or track name.

Summary comparison is keyed by `filterKey`, so lost summaries, overwrites,
wrong ownership, and wrong filter-key behavior remain visible after
normalization. Scoped summary cases set both `branch` and `filterKey` on events
to avoid depending on legacy fallback behavior. Track comparison uses track name
plus ordinal ordering and canonicalizes JSON payloads while ignoring volatile
timestamps.

The allowed-diff classifier is deliberately narrow. Capability gaps such as
event pagination or TTL support can become `unsupported`; harmless
representation differences, such as SQLite skipping an empty scoped summary,
become `allowed_diff`. Everything else is `inconsistent`.
