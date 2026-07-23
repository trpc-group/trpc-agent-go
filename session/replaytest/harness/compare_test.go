//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompareDetectsSummaryLoss(t *testing.T) {
	base := &Snapshot{SessionID: "s", Summaries: []SummaryView{{FilterKey: "", Text: "hello"}}}
	other := &Snapshot{SessionID: "s"} // summary missing
	diffs := Compare("c", "sqlite", base, other)
	require.NotEmpty(t, diffs)
	found := false
	for _, d := range diffs {
		if d.Category == "summary" && d.CompareValue == "<missing>" {
			found = true
			require.NotNil(t, d.Locator.SummaryFilterKey)
			require.Equal(t, "", *d.Locator.SummaryFilterKey)
		}
	}
	require.True(t, found)
}

func TestCompareIdenticalNoDiff(t *testing.T) {
	s := &Snapshot{SessionID: "s", State: map[string]string{"a": "b"}}
	require.Empty(t, Compare("c", "sqlite", s, &Snapshot{SessionID: "s", State: map[string]string{"a": "b"}}))
}

func TestCompareDetectsEventContentMismatch(t *testing.T) {
	base := &Snapshot{SessionID: "s", Events: []EventView{{Author: "user", Role: "user", Content: "hi"}}}
	other := &Snapshot{SessionID: "s", Events: []EventView{{Author: "user", Role: "user", Content: "bye"}}}
	diffs := Compare("c", "sqlite", base, other)
	require.NotEmpty(t, diffs)
	d := diffs[0]
	require.Equal(t, "event", d.Category)
	require.NotNil(t, d.Locator.EventIndex)
	require.Equal(t, 0, *d.Locator.EventIndex)
	require.Equal(t, "hi", d.BaselineValue)
	require.Equal(t, "bye", d.CompareValue)
}

func TestCompareDetectsEventCountMismatch(t *testing.T) {
	base := &Snapshot{SessionID: "s", Events: []EventView{{Content: "a"}, {Content: "b"}}}
	other := &Snapshot{SessionID: "s", Events: []EventView{{Content: "a"}}}
	diffs := Compare("c", "sqlite", base, other)
	found := false
	for _, d := range diffs {
		if d.Category == "event" && d.CompareValue == "<missing>" {
			found = true
			require.NotNil(t, d.Locator.EventIndex)
			require.Equal(t, 1, *d.Locator.EventIndex)
		}
	}
	require.True(t, found)
}

func TestCompareDetectsStateValueMismatch(t *testing.T) {
	base := &Snapshot{SessionID: "s", State: map[string]string{"lang": "en"}}
	other := &Snapshot{SessionID: "s", State: map[string]string{"lang": "fr"}}
	diffs := Compare("c", "sqlite", base, other)
	require.NotEmpty(t, diffs)
	require.Equal(t, "state", diffs[0].Category)
	require.Equal(t, "en", diffs[0].BaselineValue)
	require.Equal(t, "fr", diffs[0].CompareValue)
}

func TestCompareDetectsMemoryDrop(t *testing.T) {
	base := &Snapshot{SessionID: "s", Memories: []MemoryView{{ID: "mem#0", Content: "fact"}}}
	other := &Snapshot{SessionID: "s"}
	diffs := Compare("c", "sqlite", base, other)
	found := false
	for _, d := range diffs {
		if d.Category == "memory" && d.CompareValue == "<missing>" {
			found = true
			require.Equal(t, "mem#0", d.Locator.MemoryID)
		}
	}
	require.True(t, found)
}

func TestCompareMemoriesPairsByContentKindNotIndex(t *testing.T) {
	// baseline has A,B,C; other dropped B. Index pairing would misalign C,
	// key pairing must report exactly one missing (B).
	base := &Snapshot{SessionID: "s", Memories: []MemoryView{
		{ID: "mem#0", Content: "A", Kind: "fact"},
		{ID: "mem#1", Content: "B", Kind: "fact"},
		{ID: "mem#2", Content: "C", Kind: "fact"},
	}}
	other := &Snapshot{SessionID: "s", Memories: []MemoryView{
		{ID: "mem#0", Content: "A", Kind: "fact"},
		{ID: "mem#1", Content: "C", Kind: "fact"},
	}}
	diffs := Compare("c", "sqlite", base, other)
	missing := 0
	for _, d := range diffs {
		if d.Category == "memory" && d.CompareValue == missingValue {
			missing++
			require.Contains(t, d.BaselineValue, "B")
		}
	}
	require.Equal(t, 1, missing, "exactly one memory (B) is missing; got diffs %+v", diffs)
}

func TestCompareDetectsTrackPayloadMismatch(t *testing.T) {
	base := &Snapshot{SessionID: "s", Tracks: []TrackView{{Name: "steps", Payload: "one"}}}
	other := &Snapshot{SessionID: "s", Tracks: []TrackView{{Name: "steps", Payload: "two"}}}
	diffs := Compare("c", "sqlite", base, other)
	found := false
	for _, d := range diffs {
		if d.Category == "track" {
			found = true
			require.Equal(t, "steps", d.Locator.TrackName)
		}
	}
	require.True(t, found)
}
