//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// --- isAllowed coverage gaps ---

func TestComparator_isAllowed_DriftNilMaxDrift(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.events[*].timestamp", Kind: "timestamp_drift", Strategy: "allow_drift", MaxDrift: nil},
	})
	_, ok := c.isAllowed(&DiffResult{Path: "$.events[1].timestamp", Left: 1000, Right: 3000})
	if ok {
		t.Error("allow_drift with nil MaxDrift should NOT be allowed")
	}
}

func TestComparator_isAllowed_DriftNonNumeric(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.events[*].timestamp", Kind: "timestamp_drift", Strategy: "allow_drift", MaxDrift: &DriftSpec{DurationMS: 5000}},
	})
	_, ok := c.isAllowed(&DiffResult{Path: "$.events[1].timestamp", Left: "abc", Right: "def"})
	if ok {
		t.Error("allow_drift with non-numeric values should NOT be allowed")
	}
}

func TestComparator_isAllowed_ExtraKeysPositive(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.state[*]", Kind: "backend_metadata", Strategy: "allow_extra_keys"},
	})
	_, ok := c.isAllowed(&DiffResult{Path: "$.state.app:version", Kind: DiffExtraKey})
	if !ok {
		t.Error("allow_extra_keys with DiffExtraKey kind should be allowed")
	}
}

func TestComparator_isAllowed_MissingKeysPositive(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.state[*]", Kind: "backend_metadata", Strategy: "allow_missing_keys"},
	})
	_, ok := c.isAllowed(&DiffResult{Path: "$.state.app:version", Kind: DiffMissingKey})
	if !ok {
		t.Error("allow_missing_keys with DiffMissingKey kind should be allowed")
	}
}

func TestComparator_isAllowed_UnknownStrategyFallsThrough(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.events[*].id", Kind: "auto_id", Strategy: "unknown_strategy"},
	})
	_, ok := c.isAllowed(&DiffResult{Path: "$.events[0].id"})
	if ok {
		t.Error("unhandled strategy should fall through and not be allowed")
	}
}

func TestComparator_isAllowed_DriftExceedsTolerance(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.events[*].timestamp", Kind: "timestamp_drift", Strategy: "allow_drift", MaxDrift: &DriftSpec{DurationMS: 5000}},
	})
	_, ok := c.isAllowed(&DiffResult{Path: "$.events[0].timestamp", Left: 0, Right: 20000})
	if ok {
		t.Error("allow_drift with excessive drift should NOT be allowed")
	}
}

// --- toFloat coverage ---

func TestToFloat_AllTypes(t *testing.T) {
	tests := []struct {
		input any
		want  float64
		ok    bool
	}{
		{float64(3.14), 3.14, true},
		{float32(1.5), 1.5, true},
		{int(42), 42, true},
		{int64(99), 99, true},
		{int32(-7), -7, true},
		{"string", 0, false},
		{nil, 0, false},
		{true, 0, false},
	}
	for _, tc := range tests {
		got, ok := toFloat(tc.input)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("toFloat(%v) = (%v, %v), want (%v, %v)", tc.input, got, ok, tc.want, tc.ok)
		}
	}
}

// --- CompareSessions: nil session in snapshot ---

func TestCompareSessions_RightSessionNilInSnapshot(t *testing.T) {
	c := NewComparator(nil)
	left := &SessionSnapshot{Session: session.NewSession("app", "u1", "s1")}
	right := &SessionSnapshot{}
	diffs := c.CompareSessions(left, right, "a", "b")
	if len(diffs) != 1 || diffs[0].Path != "$.session" || diffs[0].Kind != DiffExtraEntry {
		t.Errorf("expected extra_entry at $.session, got %+v", diffs)
	}
}

func TestCompareSessions_LeftSessionNilInSnapshot(t *testing.T) {
	c := NewComparator(nil)
	left := &SessionSnapshot{}
	right := &SessionSnapshot{Session: session.NewSession("app", "u1", "s1")}
	diffs := c.CompareSessions(left, right, "a", "b")
	if len(diffs) != 1 || diffs[0].Path != "$.session" || diffs[0].Kind != DiffMissingEntry {
		t.Errorf("expected missing_entry at $.session, got %+v", diffs)
	}
}

// --- compareEvents: right longer than left ---

func TestCompareEvents_RightLonger(t *testing.T) {
	c := NewComparator(nil)
	left := []event.Event{{Author: "user"}}
	right := []event.Event{{Author: "user"}, {Author: "assistant"}, {Author: "tool"}}
	diffs := c.compareEvents(left, right, "$.events")
	foundExtra := false
	for _, d := range diffs {
		if d.Path == "$.events[1]" || d.Path == "$.events[2]" {
			foundExtra = true
		}
	}
	if !foundExtra {
		t.Errorf("should detect extra events in right, got %d diffs", len(diffs))
	}
}

// --- compareEvent: timestamp mismatch ---

func TestCompareEvent_TimestampDiff(t *testing.T) {
	c := NewComparator(nil)
	t1 := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 1, 15, 10, 0, 5, 0, time.UTC)
	left := &event.Event{Author: "user", Timestamp: t1,
		Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser}}}}}
	right := &event.Event{Author: "user", Timestamp: t2,
		Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser}}}}}
	diffs := c.compareEvent(left, right, "$.events[0]")
	assertHasDiffPath(t, diffs, "$.events[0].timestamp")
}

// --- compareTracks: track event timestamp mismatch ---

func TestCompareTracks_TimestampMismatch(t *testing.T) {
	c := NewComparator(nil)
	t1 := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 1, 15, 10, 0, 5, 0, time.UTC)
	left := map[session.Track]*session.TrackEvents{
		"t": {Track: "t", Events: []session.TrackEvent{{Payload: json.RawMessage(`{}`), Timestamp: t1}}},
	}
	right := map[session.Track]*session.TrackEvents{
		"t": {Track: "t", Events: []session.TrackEvent{{Payload: json.RawMessage(`{}`), Timestamp: t2}}},
	}
	diffs := c.compareTracks(left, right, "$.tracks")
	assertHasDiffPath(t, diffs, "$.tracks.t.events[0].timestamp")
}

// --- sortTrackNames: multiple tracks ---

func TestSortTrackNames_Multiple(t *testing.T) {
	tracks := []session.Track{"zebra", "alpha", "mike"}
	sortTrackNames(tracks)
	if string(tracks[0]) != "alpha" || string(tracks[1]) != "mike" || string(tracks[2]) != "zebra" {
		t.Errorf("tracks not sorted: %v", tracks)
	}
}

// --- jsonPathEscape: special characters ---

func TestJsonPathEscape_SpecialChars(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"simple", "simple"},
		{"a.b", "\"a.b\""},
		{"key[0]", "\"key[0]\""},
		{"has space", "\"has space\""},
	}
	for _, tc := range tests {
		got := jsonPathEscape(tc.key)
		if got != tc.want {
			t.Errorf("jsonPathEscape(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// --- derefPointers: right nil ---

func TestDerefPointers_RightNil(t *testing.T) {
	x := 42
	var y *int
	diffs := CompareJSON(&x, y, "$")
	if len(diffs) == 0 {
		t.Error("left ptr vs right nil should produce diffs")
	}
}

func TestDerefPointers_BothNonNil(t *testing.T) {
	x := 42
	y := 43
	diffs := CompareJSON(&x, &y, "$")
	if len(diffs) == 0 {
		t.Error("different ptr values should produce diffs")
	}
}

// --- numericDiff ---

func TestNumericDiff_IntVsInt64(t *testing.T) {
	diff, ok := numericDiff(int(100), int64(200))
	if !ok {
		t.Error("int vs int64 should be numeric")
	}
	if diff != 100 {
		t.Errorf("diff should be 100, got %v", diff)
	}
}

func TestNumericDiff_NonNumeric(t *testing.T) {
	_, ok := numericDiff("a", "b")
	if ok {
		t.Error("string comparison should not be numeric")
	}
}

// --- CompareJSON: right longer slice, right extra map key ---

func TestCompareJSON_SliceRightLonger(t *testing.T) {
	left := []any{1}
	right := []any{1, 2, 3}
	diffs := CompareJSON(left, right, "$")
	found := false
	for _, d := range diffs {
		if d.Kind == DiffExtraEntry {
			found = true
		}
	}
	if !found {
		t.Error("should detect extra entries when right slice is longer")
	}
}

func TestCompareJSON_MapExtraKeyInRight(t *testing.T) {
	left := map[string]any{"a": 1}
	right := map[string]any{"a": 1, "b": 2}
	diffs := CompareJSON(left, right, "$")
	found := false
	for _, d := range diffs {
		if d.Path == "$.b" && d.Kind == DiffExtraKey {
			found = true
		}
	}
	if !found {
		t.Error("should detect extra key in right")
	}
}
