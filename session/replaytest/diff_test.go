//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseCanonical builds a small canonical snapshot for differ tests.
func baseCanonical() *Canonical {
	return &Canonical{
		Backend: "a", Case: "c",
		Sessions: []*CSession{{
			SessionID: "s1",
			Events: []*CEvent{
				{ID: "evt#1", Author: "user", Role: "user", Content: "hello", InvocationID: "inv#1"},
				{ID: "evt#2", Author: "assistant", Role: "assistant", Content: "hi", InvocationID: "inv#1"},
			},
			State: map[string]string{"counter": "3"},
			Summaries: map[string]*CSummary{
				"": {Text: "sum", Version: 1, LastEventID: "evt#2", HasUpdatedAt: true},
			},
			Tracks: map[string][]string{
				"tool_call": {`{"status":"ok"}`},
			},
		}},
		AppState:  map[string]string{"cfg": `"dark"`},
		UserState: map[string]string{"level": "8"},
		Memories: []*CMemory{
			{ID: "mem#1", Content: "a-content", Meta: `{"kind":""}`, Order: 0},
			{ID: "mem#2", Content: "b-content", Meta: `{"kind":""}`, Order: 1},
		},
		Errors: []CError{{Step: 1, Class: "error"}},
	}
}

// nonAllowed returns the non-allowed diffs.
func nonAllowed(diffs []Diff) []Diff {
	var out []Diff
	for _, d := range diffs {
		if !d.Allowed {
			out = append(out, d)
		}
	}
	return out
}

// TestDiffIdentical expects zero diffs for identical snapshots.
func TestDiffIdentical(t *testing.T) {
	a := baseCanonical()
	b := CloneCanonical(a)
	assert.Empty(t, DiffCanonical(a, b, false))
}

// TestDiffEventContent checks field-path localization on events.
func TestDiffEventContent(t *testing.T) {
	a := baseCanonical()
	b := CloneCanonical(a)
	b.Sessions[0].Events[1].Content = "corrupted"
	diffs := nonAllowed(DiffCanonical(a, b, false))
	require.Len(t, diffs, 1)
	d := diffs[0]
	assert.Equal(t, DimEvent, d.Dimension)
	assert.Equal(t, SevMismatch, d.Severity)
	assert.Equal(t, "s1", d.SessionID)
	assert.Equal(t, 1, d.EventIndex)
	assert.Equal(t, "events[1].content", d.Path)
	assert.Equal(t, "hi", d.ValueA)
	assert.Equal(t, "corrupted", d.ValueB)
}

// TestDiffEventMissingExtra checks missing/extra event detection.
func TestDiffEventMissingExtra(t *testing.T) {
	a := baseCanonical()
	b := CloneCanonical(a)
	b.Sessions[0].Events = b.Sessions[0].Events[:1]
	diffs := nonAllowed(DiffCanonical(a, b, false))
	require.Len(t, diffs, 1)
	assert.Equal(t, SevMissing, diffs[0].Severity)
	assert.Equal(t, 1, diffs[0].EventIndex)

	c := CloneCanonical(a)
	c.Sessions[0].Events = append(c.Sessions[0].Events, &CEvent{ID: "evt#3", Content: "extra"})
	diffs = nonAllowed(DiffCanonical(a, c, false))
	require.Len(t, diffs, 1)
	assert.Equal(t, SevExtra, diffs[0].Severity)
	assert.Equal(t, 2, diffs[0].EventIndex)
}

// TestDiffSummaryDimensions covers the summary acceptance categories:
// loss, stale overwrite, filter-key error and wrong attribution.
func TestDiffSummaryDimensions(t *testing.T) {
	t.Run("loss", func(t *testing.T) {
		a := baseCanonical()
		b := CloneCanonical(a)
		delete(b.Sessions[0].Summaries, "")
		diffs := nonAllowed(DiffCanonical(a, b, false))
		require.Len(t, diffs, 1)
		assert.Equal(t, DimSummary, diffs[0].Dimension)
		assert.Equal(t, SevMissing, diffs[0].Severity)
		assert.Equal(t, "", diffs[0].FilterKey)
	})

	t.Run("stale overwrite", func(t *testing.T) {
		a := baseCanonical()
		b := CloneCanonical(a)
		b.Sessions[0].Summaries[""].Text = "STALE"
		diffs := nonAllowed(DiffCanonical(a, b, false))
		require.Len(t, diffs, 1)
		assert.Equal(t, `summaries[""].text`, diffs[0].Path)
	})

	t.Run("filter-key error", func(t *testing.T) {
		a := baseCanonical()
		b := CloneCanonical(a)
		b.Sessions[0].Summaries["wrong"] = b.Sessions[0].Summaries[""]
		delete(b.Sessions[0].Summaries, "")
		diffs := nonAllowed(DiffCanonical(a, b, false))
		require.Len(t, diffs, 2) // missing "" + extra "wrong"
		var sevs []string
		for _, d := range diffs {
			assert.Equal(t, DimSummary, d.Dimension)
			sevs = append(sevs, d.Severity)
		}
		assert.ElementsMatch(t, []string{SevMissing, SevExtra}, sevs)
	})

	t.Run("wrong attribution", func(t *testing.T) {
		a := baseCanonical()
		a.Sessions = append(a.Sessions, &CSession{SessionID: "s2"})
		b := CloneCanonical(a)
		b.Sessions[1].Summaries = b.Sessions[0].Summaries
		b.Sessions[0].Summaries = nil
		diffs := nonAllowed(DiffCanonical(a, b, false))
		require.Len(t, diffs, 2)
		// Missing on s1, extra on s2.
		assert.Equal(t, "s1", diffs[0].SessionID)
		assert.Equal(t, SevMissing, diffs[0].Severity)
		assert.Equal(t, "s2", diffs[1].SessionID)
		assert.Equal(t, SevExtra, diffs[1].Severity)
	})
}

// TestDiffState checks state map diffs including app/user scopes.
func TestDiffState(t *testing.T) {
	a := baseCanonical()
	b := CloneCanonical(a)
	b.Sessions[0].State["counter"] = "2"
	delete(b.AppState, "cfg")
	b.UserState["level"] = "9"
	diffs := nonAllowed(DiffCanonical(a, b, false))
	require.Len(t, diffs, 3)
	paths := []string{diffs[0].Path, diffs[1].Path, diffs[2].Path}
	assert.ElementsMatch(t,
		[]string{`state["counter"]`, `app_state["cfg"]`, `user_state["level"]`}, paths)
}

// TestDiffMemory checks memory set, field and duplicate detection.
func TestDiffMemory(t *testing.T) {
	t.Run("field mismatch located by memory id", func(t *testing.T) {
		a := baseCanonical()
		b := CloneCanonical(a)
		b.Memories[0].Meta = `{"kind":"episode"}`
		diffs := nonAllowed(DiffCanonical(a, b, false))
		require.Len(t, diffs, 1)
		assert.Equal(t, "mem#1", diffs[0].MemoryID)
		assert.Equal(t, "memories[0].meta", diffs[0].Path)
	})

	t.Run("duplicate", func(t *testing.T) {
		a := baseCanonical()
		b := CloneCanonical(a)
		dup := *b.Memories[0]
		b.Memories = append(b.Memories, &dup)
		diffs := nonAllowed(DiffCanonical(a, b, false))
		require.Len(t, diffs, 1)
		assert.Equal(t, SevExtra, diffs[0].Severity)
	})
}

// TestDiffTrack checks track diffs with track-name localization.
func TestDiffTrack(t *testing.T) {
	a := baseCanonical()
	b := CloneCanonical(a)
	b.Sessions[0].Tracks["tool_call"][0] = `{"status":"error"}`
	b.Sessions[0].Tracks["subtask"] = []string{`{"x":1}`}
	diffs := nonAllowed(DiffCanonical(a, b, false))
	require.Len(t, diffs, 2)
	byTrack := map[string]Diff{}
	for _, d := range diffs {
		byTrack[d.TrackName] = d
	}
	assert.Equal(t, `tracks["tool_call"][0].payload`, byTrack["tool_call"].Path)
	assert.Equal(t, SevMismatch, byTrack["tool_call"].Severity)
	assert.Equal(t, SevExtra, byTrack["subtask"].Severity)
}

// TestDiffUnordered checks multiset plus per-branch comparison.
func TestDiffUnordered(t *testing.T) {
	build := func() *Canonical {
		return &Canonical{
			Backend: "a", Case: "conc",
			Sessions: []*CSession{{
				SessionID: "s1",
				Events: []*CEvent{
					{ID: "evt#1", Content: "u", Branch: "", Role: "user"},
					{ID: "evt#2", Content: "w1-1", Branch: "w1"},
					{ID: "evt#3", Content: "w2-1", Branch: "w2"},
					{ID: "evt#4", Content: "w1-2", Branch: "w1"},
				},
			}},
		}
	}

	// Interleaving across branches is not a diff in unordered mode.
	a := build()
	b := build()
	evs := b.Sessions[0].Events
	// Re-interleave to [u, w2-1, w1-1, w1-2]: same multiset, same
	// per-branch order, different global order.
	evs[1], evs[2] = evs[2], evs[1]
	assert.Empty(t, nonAllowed(DiffCanonical(a, b, true)))

	// Per-branch reorder is detected even in unordered mode: the two w1
	// events keep their identity but swap positions.
	c := build()
	c.Sessions[0].Events[1], c.Sessions[0].Events[3] =
		c.Sessions[0].Events[3], c.Sessions[0].Events[1]
	diffs := nonAllowed(DiffCanonical(a, c, true))
	require.Len(t, diffs, 2)
	for _, d := range diffs {
		assert.Equal(t, DimEvent, d.Dimension)
		assert.Contains(t, d.Path, `events(branch="w1")`)
	}
}

// TestDiffErrors checks error-class comparison.
func TestDiffErrors(t *testing.T) {
	a := baseCanonical()
	b := CloneCanonical(a)
	b.Errors[0].Class = "nil"
	diffs := nonAllowed(DiffCanonical(a, b, false))
	require.Len(t, diffs, 1)
	assert.Equal(t, DimError, diffs[0].Dimension)
	assert.Equal(t, "errors[0]", diffs[0].Path)
}

// TestDiffMemoryOrderAllowed checks that a differing memory listing order
// is reported as an allowed note, not a failure.
func TestDiffMemoryOrderAllowed(t *testing.T) {
	a := baseCanonical()
	b := CloneCanonical(a)
	b.Memories[0].Order, b.Memories[1].Order = b.Memories[1].Order, b.Memories[0].Order
	diffs := DiffCanonical(a, b, false)
	require.Empty(t, nonAllowed(diffs), "order-only difference must not fail")
	require.Len(t, diffs, 1)
	assert.True(t, diffs[0].Allowed)
	assert.Equal(t, DimMemory, diffs[0].Dimension)
	assert.Equal(t, SevOrder, diffs[0].Severity)
	assert.Equal(t, "memories.order", diffs[0].Path)
}
