//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import "strings"

// AllowedDiffRule declares a difference that backends are permitted to have,
// together with the reason it is permitted.
//
// The list is short on purpose. Every entry is a place where the harness has
// decided not to fail a run, so each one is a hole in the guarantee and has to
// earn its place. Allowed differences are still recorded in the report, which
// keeps them visible rather than silently dropped.
type AllowedDiffRule struct {
	// Path is the projection path the rule covers. It matches the path itself
	// and anything nested beneath it, so a rule on a slice covers its elements
	// and its length.
	Path string
	// Reason states why backends may legitimately differ here.
	Reason string
}

// allowedDiffRules is the complete allow list.
var allowedDiffRules = []AllowedDiffRule{
	{
		Path: "memories.readOrder",
		Reason: "ReadMemories orders by an update timestamp the service assigns from the wall clock. " +
			"Writes that land in the same tick tie, and backends break ties differently. " +
			"Membership, identifiers, content and topics are still compared exactly through memories.entries.",
	},
}

// AllowedDiffRules returns the allow list so that documentation and reports
// can render the same source of truth the comparator uses.
func AllowedDiffRules() []AllowedDiffRule {
	return append([]AllowedDiffRule(nil), allowedDiffRules...)
}

// KnownDivergence records a difference that is real rather than legitimate.
//
// It is kept separate from [AllowedDiffRule] on purpose. An allowed difference
// says the backends are both right; a known divergence says they are not, and
// that the harness is reporting it rather than failing the build while the
// question is open. Collapsing the two categories would turn this file into
// the place where inconvenient findings go to be forgotten.
type KnownDivergence struct {
	// Path is the projection path, matched like AllowedDiffRule.Path.
	Path string
	// Backend is the backend that differs from the baseline.
	Backend string
	// Note states what was observed, what the suspected mechanism is, and how
	// confident the harness is about it.
	Note string
}

// knownDivergences is the complete tracked list.
var knownDivergences = []KnownDivergence{
	{
		Path:    `sessions[ref="replay/u-state/s-state"].state[key="lang"]`,
		Backend: "redis",
		Note: "An event state delta whose value is nil leaves the previous value in place instead of storing nil. " +
			"session/inmemory and session/sqlite both store nil, matching session.ApplyEventStateDelta. " +
			"The redis hashidx path applies deltas inside a Lua script guarded by next(stateDelta) ~= nil. " +
			"Observed against miniredis, whose Lua decodes JSON null to a Lua nil; a Lua table cannot hold a nil " +
			"value, so the key disappears, the table reads as empty and the guard skips the whole update. " +
			"Real Redis decodes null to a cjson.null sentinel and would likely apply the delta, so this may be an " +
			"emulation difference rather than a backend defect. Recorded rather than asserted until it is " +
			"confirmed against a real Redis server through the integration mode.",
	},
	{
		Path:    `sessions[ref="replay/u-interleaved/s-interleaved"].events`,
		Backend: "redis",
		Note: "Events appended out of timestamp order read back in timestamp order rather than append order. " +
			"session/inmemory appends to a slice and session/sqlite inserts rows read back by insertion, so both " +
			"preserve the order the caller wrote. The redis hashidx path indexes events with " +
			"ZADD <key> <event timestamp> <eventID> and reads that sorted set back by score, so the timestamp " +
			"decides the order. This is the reordering the issue describes: a caller that appends a late-arriving " +
			"tool result carrying an earlier timestamp replays it in a different position depending on the " +
			"backend. The mechanism is ordinary Redis sorted-set behavior rather than an artifact of the " +
			"in-process server used by the lightweight mode.",
	},
	{
		Path:    `sessions[ref="replay/u-retry/s-retry"].events.length`,
		Backend: "redis",
		Note: "Re-appending an event with an identifier that was already stored collapses the two writes, while " +
			"session/inmemory and session/sqlite keep both. The redis hashidx path stores events with " +
			"HSET <key> <eventID> <json>, so a repeated identifier overwrites the field; sqlite inserts a new row " +
			"with no uniqueness constraint on the event identifier, and inmemory appends unconditionally. " +
			"session.Service does not document whether AppendEvent is idempotent, so neither behavior violates a " +
			"stated contract, but a retry after a crash means different things depending on where the data lives.",
	},
}

// KnownDivergences returns the tracked list for documentation and reports.
func KnownDivergences() []KnownDivergence {
	return append([]KnownDivergence(nil), knownDivergences...)
}

// knownDivergence reports whether a path and backend are tracked.
func knownDivergence(path, backend string) (string, bool) {
	for _, k := range knownDivergences {
		if k.Backend == backend && matchesRulePath(path, k.Path) {
			return k.Note, true
		}
	}
	return "", false
}

// allowedDiff reports whether a path is covered by the allow list.
func allowedDiff(path string) (string, bool) {
	for _, rule := range allowedDiffRules {
		if matchesRulePath(path, rule.Path) {
			return rule.Reason, true
		}
	}
	return "", false
}

// matchesRulePath reports whether path is the rule path or nested inside it.
//
// Matching is anchored at a path boundary so that a rule on "memories.read"
// cannot accidentally swallow "memories.readOrder".
func matchesRulePath(path, rulePath string) bool {
	if path == rulePath {
		return true
	}
	if !strings.HasPrefix(path, rulePath) {
		return false
	}
	switch path[len(rulePath)] {
	case '.', '[':
		return true
	default:
		return false
	}
}
