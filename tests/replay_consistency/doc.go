package replayconsistency

// Package replayconsistency provides a multi-backend replay consistency test
// harness for trpc-agent-go session, memory, summary, and track subsystems.
//
// # Design
//
// The harness replayes identical sequences of operations (event append, state
// mutation, memory write, summary creation, track event recording) against a
// baseline backend (typically InMemory) and one or more test backends (SQLite,
// Redis, PostgreSQL, MySQL, or ClickHouse). After replay, each backend's
// session state, events, summaries, tracks, and memories are read back and
// normalized into deterministic NormalizedSnapshot structs for comparison.
//
// # Normalization Strategy
//
// The Normalizer strips or canonicalizes auto-generated fields to enable
// structural comparison. Event IDs, memory IDs, and timestamps are compared
// with allowed-diff tags since they are backend-generated. State values are
// base64-encoded and JSON payloads are re-marshaled to canonicalize key
// ordering. Maps are sorted by key before comparison. Float scores are
// rounded to a fixed number of decimal places. Backend-private metadata
// fields (e.g. ServiceMeta, Hash) are stripped.
//
// # Comparison Strategy
//
// The Comparator performs position-by-position comparison of sorted arrays.
// For events, it compares author, role, content, tool call ID/name, branch,
// tag, filter key, state delta, and extensions. For summaries, it compares
// text, filter key, boundary metadata, and version. For tracks, it compares
// track name, payload (as canonical JSON), and timestamp. Memory comparison
// sorts entries by ID and compares content, topics, kind, and metadata.
//
// # Allowed Diff Rules
//
// Diffs are classified as allowed or disallowed. Allowed diffs include:
// auto-generated IDs, timestamps, float scores (within epsilon), map key
// ordering, and backend-specific metadata variations. Backends that lack
// track support (ClickHouse) mark track diffs as allowed. Case-level
// AllowedDiffPatterns support wildcard path matching for custom rules.
//
// # Backend Integration
//
// Lightweight mode (InMemory + SQLite) runs without external infrastructure.
// Extended backends are enabled via environment variables:
// REPLAY_ENABLE_REDIS, REPLAY_ENABLE_POSTGRES, REPLAY_ENABLE_MYSQL, and
// REPLAY_ENABLE_CLICKHOUSE. Each requires corresponding connection parameters
// (HOST, PORT, USER, PASSWORD, DATABASE). Backend pairs with missing env vars
// are automatically skipped.
