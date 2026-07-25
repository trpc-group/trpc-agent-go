//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

/*
Package replaytest provides a cross-backend replay consistency test framework for Session, Memory, Summary, and Track backends in trpc-agent-go.

# Design

The framework defines a JSON-based DSL (Spec) that describes a sequence of operations (create sessions, append events, update state, write memories, generate summaries, append track events, etc.).
A Harness executes the same Spec against multiple backend implementations simultaneously, using the same keys (session.Key, memory.UserKey) so results are comparable.

After execution, a Normalizer chain canonicalizes the raw snapshots by stripping backend-private metadata, replacing auto-generated IDs and timestamps with deterministic placeholders, normalizing JSON field order and map iteration order, and handling floating-point precision.

A Comparator then compares the normalized snapshots field by field across backends.
Differences are classified by kind (value_mismatch, missing_key, extra_key, type_mismatch, etc.) and severity (error, warning, info).
AllowedDiff rules suppress expected differences (auto-generated IDs, timestamp drift, backend-private metadata).

# Normalization Strategy

Auto-generated IDs (event IDs, invocation IDs, memory IDs) are replaced with sequential placeholders like <evt-id-0>.
Timestamps are truncated to second precision to absorb sub-second drift.
JSON fields (Payload in track events, Extensions in events) are unmarshalled and re-marshalled to ensure deterministic key ordering.
Float scores are rounded to 4 decimal places.
Nil/empty slices and maps are normalized to empty values.
Memory EventTime is normalized together with CreatedAt/UpdatedAt timestamps.
For concurrent specs (tagged "concurrent"), events are sorted by a stable composite key (author → tag → filterKey → content) to provide order-independent comparison.

# Summary Comparison Strategy

Summaries are compared by filter key.
For each filter key, the Summary text, Topics list, and Boundary metadata (Version, FilterKey, CutoffAt, LastEventID) are compared.
Missing filter keys are treated as error-level diffs (summary loss detection).
Mismatched filter keys in boundaries are flagged as "summary filter-key error" at error severity.
Boundary version and lastEventID mismatches are warning-level.

# Track Comparison Strategy

Tracks are compared by track name.
Within each track, events are aligned by index and compared for Payload content (after JSON normalization) and Timestamps (after normalization).
Missing tracks or event count mismatches are error-level diffs.

# Memory Comparison Strategy

Memories are matched by ID across backends.
Each entry is compared for Memory text, Topics (order-independent), Kind, Participants, Location, EventTime, and Score.
EventTime is normalized by the timestamp normalizer before comparison.
Missing or extra memory entries are error-level diffs.

# Injection Testing

The injection_test.go file provides 29 sub-test cases that systematically inject inconsistencies (event content/author/role, state values, summary text/filter-key/loss, track payload, memory text/kind/score, etc.) and verify 100% detection by the comparator.
This satisfies the acceptance criterion that all publicly provided replay cases must detect artificially injected inconsistencies.

# AllowedDiff Rules

Rules can suppress expected differences:
  - "ignore": completely suppress (auto IDs, backend metadata)
  - "allow_drift": allow within tolerance (timestamp drift ≤5s, float epsilon)
  - "allow_extra_keys": allow keys only in right backend (soft-delete artifacts)
  - "allow_missing_keys": allow keys only in left backend (unsupported features)

Rules apply at three levels: global defaults, per-spec, or per-backend-pair.

# Diff Report Localization

Each VerificationResult carries:
  - SessionKey: identifies the session (app, user, session ID)
  - SummaryFilterKey: comma-separated list of summary filter keys from the reference snapshot, for pinning down summary-level issues.
  - TrackName: comma-separated list of track names from the reference snapshot, for locating track-level issues.
  - MemoryID: comma-separated list of memory IDs from the reference snapshot, for identifying memory-level issues.

Combined with the JSONPath in DiffResult.Path (e.g., $.events[3].author, $.summaries.branch-a.boundary.filterKey, $.tracks.mytrack.events[0].payload), the report can pinpoint inconsistencies to exact event indices, filter keys, track names, and memory IDs.

# Backend Integration

Backends are registered via RegisterSessionFactory/RegisterMemoryFactory.
The framework ships with inmemory backends built-in.
SQLite support requires importing the session/sqlite and memory/sqlite sub-modules and registering their factories.
Optional backends (Redis, Postgres, MySQL, ClickHouse) are enabled via environment variables in integration mode (see caserunner package).

# Running Tests

Lightweight mode (inmemory + sqlite, ≤30s):

	cd replaytest/caserunner && go test -run Lightweight -timeout 30s -v

Integration mode (env-var gated):

	REPLAY_SESSION_BACKENDS=inmemory,sqlite \
	REPLAY_MEMORY_BACKENDS=inmemory,sqlite \
	go test -run Integration -timeout 120s -v

Unit tests (normalizer, comparator, report, injection):

	go test ./replaytest/ -v
*/
package replaytest
