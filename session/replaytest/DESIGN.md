# Replaytest Design Rationale

`replaytest` drives each backend with the same typed `ReplayCase` sequence and
compares normalized snapshots rather than backend-specific storage records. The
normalizer replaces generated event IDs with stable replay keys, converts
timestamps to UTC, canonicalizes JSON payloads, removes private state keys,
preserves scoped state prefixes, and sorts memory topics and participants.

Summary replay uses a deterministic fake summarizer so tests can verify
persistence, filter-key ownership, overwrite behavior, truncation boundaries,
and the asynchronous enqueue pipeline without calling an external model. Track
replay compares normalized track names, payload JSON, timestamps, event counts,
and stored track event order.

Concurrent replay accepts different cross-branch interleavings while enforcing
branch-local order, complete event-key sets, duplicate-key detection, and branch
ownership. Memory comparison is strict when retrieval profiles match exactly;
when algorithms differ, sentinel cases verify target recall instead of assuming
identical ranking or score scales.

Allowed diff rules are explicit and path-scoped. They support ignore,
same-type, not-empty, and numeric-delta matching, and they are applied only
after a real comparison has produced a diff. Backend integration is dependency
injected through `NamedBackend`: lightweight runs use InMemory, SQLite is wired
from the separate `session/replaytest/sqlite` module, and Redis, PostgreSQL,
MySQL, and ClickHouse remain placeholder factories until real adapter modules
are provided.
