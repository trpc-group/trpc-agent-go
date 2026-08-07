//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// recursiveDiff
// ---------------------------------------------------------------------------

func TestRecursiveDiff_EqualValues(t *testing.T) {
	diffs := recursiveDiff("$.x", map[string]any{"a": 1}, map[string]any{"a": 1})
	assert.Empty(t, diffs)
}

func TestRecursiveDiff_MissingKey(t *testing.T) {
	left := map[string]any{"a": 1, "b": 2}
	right := map[string]any{"a": 1}
	diffs := recursiveDiff("$.x", left, right)
	require.Len(t, diffs, 1)
	assert.Equal(t, "$.x.b", diffs[0].Path)
	assert.Equal(t, 2, diffs[0].Left)
	assert.IsType(t, map[string]string{}, diffs[0].Right)
}

func TestRecursiveDiff_ExtraKey(t *testing.T) {
	left := map[string]any{"a": 1}
	right := map[string]any{"a": 1, "b": 2}
	diffs := recursiveDiff("$.x", left, right)
	require.Len(t, diffs, 1)
	assert.Equal(t, "$.x.b", diffs[0].Path)
	assert.IsType(t, map[string]string{}, diffs[0].Left)
	assert.Equal(t, 2, diffs[0].Right)
}

func TestRecursiveDiff_ValueMismatch(t *testing.T) {
	left := map[string]any{"a": "hello"}
	right := map[string]any{"a": "world"}
	diffs := recursiveDiff("$.x", left, right)
	require.Len(t, diffs, 1)
	assert.Equal(t, "$.x.a", diffs[0].Path)
	assert.Equal(t, "hello", diffs[0].Left)
	assert.Equal(t, "world", diffs[0].Right)
}

func TestRecursiveDiff_ListLengthDiff(t *testing.T) {
	left := []any{"a", "b"}
	right := []any{"a"}
	diffs := recursiveDiff("$.x", left, right)
	require.Len(t, diffs, 1)
	assert.Equal(t, "$.x[1]", diffs[0].Path)
	assert.Equal(t, "b", diffs[0].Left)
}

func TestRecursiveDiff_NestedDiff(t *testing.T) {
	left := map[string]any{
		"outer": map[string]any{"inner": "left"},
	}
	right := map[string]any{
		"outer": map[string]any{"inner": "right"},
	}
	diffs := recursiveDiff("$.x", left, right)
	require.Len(t, diffs, 1)
	assert.Equal(t, "$.x.outer.inner", diffs[0].Path)
	assert.Equal(t, "left", diffs[0].Left)
	assert.Equal(t, "right", diffs[0].Right)
}

func TestRecursiveDiff_TypeMismatch(t *testing.T) {
	left := "string"
	right := 42
	diffs := recursiveDiff("$.x", left, right)
	require.Len(t, diffs, 1)
	assert.Equal(t, "$.x", diffs[0].Path)
}

// ---------------------------------------------------------------------------
// CompareSnapshots (integration of all sections)
// ---------------------------------------------------------------------------

func TestCompareSnapshots_Identical(t *testing.T) {
	a := &ReplaySnapshot{
		BackendName: "be_a",
		Session:     sessionSnapshot{ID: "s1", App: "app", UserID: "u"},
		Events:      []map[string]any{{"author": "user"}},
		State:       map[string]any{"k": "v"},
	}
	b := &ReplaySnapshot{
		BackendName: "be_b",
		Session:     sessionSnapshot{ID: "s1", App: "app", UserID: "u"},
		Events:      []map[string]any{{"author": "user"}},
		State:       map[string]any{"k": "v"},
	}
	diffs := CompareSnapshots("case", a, b, nil)
	assert.Empty(t, diffs)
}

func TestCompareSnapshots_EventDiff(t *testing.T) {
	a := &ReplaySnapshot{
		BackendName: "be_a",
		Events:      []map[string]any{{"author": "user"}},
	}
	b := &ReplaySnapshot{
		BackendName: "be_b",
		Events:      []map[string]any{{"author": "agent"}},
	}
	diffs := CompareSnapshots("case", a, b, nil)
	require.Len(t, diffs, 1)
	assert.Equal(t, "events", diffs[0].Section)
	assert.Equal(t, "$.events[0].author", diffs[0].Path)
	assert.Equal(t, "case", diffs[0].Case)
}

// ---------------------------------------------------------------------------
// wildcardMatch
// ---------------------------------------------------------------------------

func TestWildcardMatch_Exact(t *testing.T) {
	assert.True(t, wildcardMatch("$.events[0].content", "$.events[0].content"))
	assert.False(t, wildcardMatch("$.events[0].content", "$.events[1].content"))
}

func TestWildcardMatch_StarWildcard(t *testing.T) {
	// Single-star as full match.
	assert.True(t, wildcardMatch("*", "$.events[0].content"))
	// Star as substring wildcard — the brackets are literal parts of the path,
	// so the pattern must include them.
	assert.True(t, wildcardMatch("$.events[*].content", "$.events[0].content"))
	assert.True(t, wildcardMatch("$.events[*].content", "$.events[99].content"))
	assert.False(t, wildcardMatch("$.events[*].content", "$.state.key"))
}

func TestWildcardMatch_MultiStar(t *testing.T) {
	assert.True(t, wildcardMatch("$.events[*].*", "$.events[3].author"))
	assert.True(t, wildcardMatch("$.events[*].*", "$.events[3].filterKey"))
	assert.False(t, wildcardMatch("$.events[*].*", "$.state.k"))
}

func TestWildcardMatch_PrefixSuffix(t *testing.T) {
	// Pattern with multiple wildcard segments.
	assert.True(t, wildcardMatch("$.events[*].choices[*].message.*", "$.events[0].choices[0].message.content"))
	assert.True(t, wildcardMatch("$.events[*].choices[*].message.*", "$.events[2].choices[1].message.tool_id"))
}

// ---------------------------------------------------------------------------
// applyAllowedRules
// ---------------------------------------------------------------------------

func TestApplyAllowedRules_MarksAllowed(t *testing.T) {
	rules := []AllowedDiffRule{
		{
			Section:  "events",
			Path:     "$.events[0].content",
			BackendA: "in_memory",
			BackendB: "sqlite",
			Reason:   "test reason",
		},
	}
	diffs := []DiffEntry{
		{Section: "events", Path: "$.events[0].content", BackendA: "in_memory", BackendB: "sqlite", Allowed: false},
	}
	applyAllowedRules(diffs, rules)
	assert.True(t, diffs[0].Allowed)
	assert.Equal(t, "test reason", diffs[0].Reason)
}

func TestApplyAllowedRules_NoMatch(t *testing.T) {
	rules := []AllowedDiffRule{
		{Section: "events", Path: "$.events[0].content", BackendA: "in_memory", BackendB: "sqlite", Reason: "r"},
	}
	diffs := []DiffEntry{
		{Section: "state", Path: "$.state.k", BackendA: "in_memory", BackendB: "sqlite", Allowed: false},
	}
	applyAllowedRules(diffs, rules)
	assert.False(t, diffs[0].Allowed)
}

func TestApplyAllowedRules_SymmetricBackends(t *testing.T) {
	rules := []AllowedDiffRule{
		{Section: "events", Path: "*", BackendA: "in_memory", BackendB: "sqlite", Reason: "order"},
	}
	diffs := []DiffEntry{
		{Section: "events", Path: "$.events[0].content", BackendA: "sqlite", BackendB: "in_memory", Allowed: false},
	}
	applyAllowedRules(diffs, rules)
	assert.True(t, diffs[0].Allowed)
}

// ---------------------------------------------------------------------------
// buildDiffContext
// ---------------------------------------------------------------------------

func TestBuildDiffContext_EventIndex(t *testing.T) {
	ctx := buildDiffContext("events", "$.events[5].content",
		&ReplaySnapshot{}, &ReplaySnapshot{})
	require.NotNil(t, ctx)
	assert.Equal(t, 5, ctx["event_index"])
}

func TestBuildDiffContext_SummaryFilterKey_Quoted(t *testing.T) {
	a := &ReplaySnapshot{
		Summaries: map[string]summarySnapshot{
			"chat": {Summary: "s"},
		},
	}
	b := &ReplaySnapshot{
		Summaries: map[string]summarySnapshot{
			"chat": {Summary: "s"},
		},
	}
	ctx := buildDiffContext("summary", `$.summary["chat"].summary`, a, b)
	require.NotNil(t, ctx)
	assert.Equal(t, "chat", ctx["summary_filter_key"])
}

func TestBuildDiffContext_SummaryFilterKey_Dot(t *testing.T) {
	ctx := buildDiffContext("summary", `$.summary.chat.summary`,
		&ReplaySnapshot{},
		&ReplaySnapshot{})
	require.NotNil(t, ctx)
	assert.Equal(t, "chat", ctx["summary_filter_key"])
}

func TestBuildDiffContext_TrackName(t *testing.T) {
	a := &ReplaySnapshot{
		Tracks: []trackSnap{
			{Name: "exec"},
			{Name: "errors"},
		},
	}
	ctx := buildDiffContext("tracks", "$.tracks[1].events[0].payload",
		a, &ReplaySnapshot{})
	require.NotNil(t, ctx)
	assert.Equal(t, "errors", ctx["track_name"])
	assert.Equal(t, 0, ctx["track_event_index"])
}

func TestBuildDiffContext_UnknownSection(t *testing.T) {
	ctx := buildDiffContext("unknown", "$.foo.bar",
		&ReplaySnapshot{}, &ReplaySnapshot{})
	assert.Nil(t, ctx)
}

// ---------------------------------------------------------------------------
// path helpers
// ---------------------------------------------------------------------------

func TestParseJSONPathIndex(t *testing.T) {
	idx, ok := parseJSONPathIndex("$.events[3].content", "$.events")
	assert.True(t, ok)
	assert.Equal(t, 3, idx)
}

func TestParseJSONPathIndex_NoMatch(t *testing.T) {
	_, ok := parseJSONPathIndex("$.state.k", "$.events")
	assert.False(t, ok)
}

func TestParseSummaryFilterKey_Quoted(t *testing.T) {
	key, ok := parseSummaryFilterKey(`$.summary["chat"].text`)
	assert.True(t, ok)
	assert.Equal(t, "chat", key)
}

func TestParseSummaryFilterKey_Dot(t *testing.T) {
	key, ok := parseSummaryFilterKey("$.summary.my_filter.text")
	assert.True(t, ok)
	assert.Equal(t, "my_filter", key)
}
