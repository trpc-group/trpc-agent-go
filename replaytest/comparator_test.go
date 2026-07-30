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

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Comparator.isAllowed

func TestComparator_isAllowed_ExactPathIgnore(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.events[0].author", Kind: "auto_id", Strategy: "ignore"},
	})
	_, ok := c.isAllowed(&DiffResult{Path: "$.events[0].author"})
	if !ok {
		t.Error("exact path should be allowed")
	}
}

func TestComparator_isAllowed_WildcardPath(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.events[*].id", Kind: "auto_id", Strategy: "ignore"},
	})
	_, ok := c.isAllowed(&DiffResult{Path: "$.events[3].id"})
	if !ok {
		t.Error("wildcard path should match any index")
	}
}

func TestComparator_isAllowed_DifferentPathRejected(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.events[*].id", Kind: "auto_id", Strategy: "ignore"},
	})
	_, ok := c.isAllowed(&DiffResult{Path: "$.events[0].author"})
	if ok {
		t.Error("different path should not match")
	}
}

func TestComparator_isAllowed_AllowDrift(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.events[*].timestamp", Kind: "timestamp_drift", Strategy: "allow_drift", MaxDrift: &DriftSpec{DurationMS: 5000}},
	})
	// Drift of 2 seconds is within 5000ms → allowed.
	_, ok := c.isAllowed(&DiffResult{Path: "$.events[1].timestamp", Left: 1000, Right: 3000})
	if !ok {
		t.Error("drift within tolerance should be allowed")
	}
}

func TestComparator_isAllowed_DriftExceeded(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.events[*].timestamp", Kind: "timestamp_drift", Strategy: "allow_drift", MaxDrift: &DriftSpec{DurationMS: 5000}},
	})
	// Drift of 10 seconds exceeds 5000ms → NOT allowed.
	_, ok := c.isAllowed(&DiffResult{Path: "$.events[0].timestamp", Left: 0, Right: 10000})
	if ok {
		t.Error("drift exceeding tolerance should NOT be allowed")
	}
}

// Comparator.CompareSessions — nil / empty cases

func TestCompareSessions_BothNil(t *testing.T) {
	c := NewComparator(nil)
	diffs := c.CompareSessions(nil, nil, "a", "b")
	if len(diffs) != 0 {
		t.Errorf("both nil should have 0 diffs, got %d", len(diffs))
	}
}

func TestCompareSessions_LeftNil(t *testing.T) {
	c := NewComparator(nil)
	right := &SessionSnapshot{Session: session.NewSession("app", "u1", "s1")}
	diffs := c.CompareSessions(nil, right, "a", "b")
	if len(diffs) == 0 {
		t.Error("left nil should report missing entry")
	}
}

func TestCompareSessions_RightNil(t *testing.T) {
	c := NewComparator(nil)
	left := &SessionSnapshot{Session: session.NewSession("app", "u1", "s1")}
	diffs := c.CompareSessions(left, nil, "a", "b")
	if len(diffs) == 0 {
		t.Error("right nil should report extra entry")
	}
}

func TestCompareSessions_EmptySessions(t *testing.T) {
	c := NewComparator(nil)
	left := &SessionSnapshot{Session: session.NewSession("app", "u1", "s1")}
	right := &SessionSnapshot{Session: session.NewSession("app", "u1", "s1")}
	diffs := c.CompareSessions(left, right, "a", "b")
	if len(diffs) != 0 {
		t.Errorf("identical empty sessions should have 0 diffs, got %d", len(diffs))
	}
}

// Comparator.compareEvents

func TestCompareEvents_CountMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := []event.Event{
		{Author: "user"}, {Author: "assistant"}, {Author: "user"},
	}
	right := []event.Event{
		{Author: "user"},
	}
	diffs := c.compareEvents(left, right, "$.events")
	if len(diffs) < 2 {
		t.Fatalf("expected at least 2 diffs (count + extra), got %d", len(diffs))
	}
}

func TestCompareEvents_EqualEmpty(t *testing.T) {
	c := NewComparator(nil)
	diffs := c.compareEvents(nil, nil, "$.events")
	if len(diffs) != 0 {
		t.Errorf("both nil should be 0 diffs, got %d", len(diffs))
	}
	diffs = c.compareEvents([]event.Event{}, []event.Event{}, "$.events")
	if len(diffs) != 0 {
		t.Errorf("both empty should be 0 diffs, got %d", len(diffs))
	}
}

// Comparator.compareEvent — individual event fields

func TestCompareEvent_AllFields(t *testing.T) {
	c := NewComparator(nil)
	left := &event.Event{
		Author: "user", Branch: "main", Tag: "greeting", FilterKey: "main",
		StateDelta: map[string][]byte{"k1": []byte("v1")},
		Response: &model.Response{
			Choices: []model.Choice{{Index: 0, Message: model.Message{Role: model.RoleUser, Content: "hi"}}},
		},
	}
	right := &event.Event{
		Author: "assistant", Branch: "dev", Tag: "response", FilterKey: "dev",
		StateDelta: map[string][]byte{"k2": []byte("v2")},
		Response: &model.Response{
			Choices: []model.Choice{{Index: 0, Message: model.Message{Role: model.RoleAssistant, Content: "hello"}}},
		},
	}
	diffs := c.compareEvent(left, right, "$.events[0]")

	// Should have diffs for author, branch, tag, filterKey, stateDelta (x2), response.content, response.role.
	paths := make(map[string]bool)
	for _, d := range diffs {
		paths[d.Path] = true
	}
	mustHave := []string{
		"$.events[0].author",
		"$.events[0].branch",
		"$.events[0].tag",
		"$.events[0].filterKey",
		"$.events[0].response.choices[0].message.role",
		"$.events[0].response.choices[0].message.content",
	}
	for _, p := range mustHave {
		if !paths[p] {
			t.Errorf("missing diff for %s", p)
		}
	}
	t.Logf("detected %d diffs", len(diffs))
	for _, d := range diffs {
		t.Logf("  %s | %s", d.Path, d.Message)
	}
}

func TestCompareEvent_ResponsePresence(t *testing.T) {
	c := NewComparator(nil)
	left := &event.Event{Author: "user", Response: &model.Response{Choices: []model.Choice{{}}}}
	right := &event.Event{Author: "user", Response: nil}
	diffs := c.compareEvent(left, right, "$.events[0]")
	hasPresence := false
	for _, d := range diffs {
		if d.Path == "$.events[0].response" {
			hasPresence = true
		}
	}
	if !hasPresence {
		t.Error("should detect response presence mismatch")
	}
}

// Comparator.CompareStateMaps

func TestCompareStateMaps_ValueMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := session.StateMap{"key": []byte("left_val")}
	right := session.StateMap{"key": []byte("right_val")}
	diffs := c.compareStateMaps(left, right, "$.state")
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].Kind != DiffValueMismatch {
		t.Errorf("expected value_mismatch, got %s", diffs[0].Kind)
	}
}

func TestCompareStateMaps_ExtraAndMissing(t *testing.T) {
	c := NewComparator(nil)
	left := session.StateMap{"only_left": []byte("x"), "shared": []byte("s")}
	right := session.StateMap{"only_right": []byte("y"), "shared": []byte("s")}
	diffs := c.compareStateMaps(left, right, "$.state")
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs (extra + missing), got %d", len(diffs))
	}
}

func TestCompareStateMaps_BothEmpty(t *testing.T) {
	c := NewComparator(nil)
	diffs := c.compareStateMaps(session.StateMap{}, session.StateMap{}, "$.state")
	if len(diffs) != 0 {
		t.Errorf("both empty should be 0 diffs, got %d", len(diffs))
	}
}

func TestCompareStateMaps_ByteLevelEquality(t *testing.T) {
	c := NewComparator(nil)
	left := session.StateMap{"k": []byte{0, 1, 2}}
	right := session.StateMap{"k": []byte{0, 1, 3}}
	diffs := c.compareStateMaps(left, right, "$.state")
	if len(diffs) != 1 {
		t.Fatalf("byte-level diff should be detected, got %d", len(diffs))
	}
}

// Comparator.compareSummaries

func TestCompareSummaries_TextMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := map[string]*session.Summary{"": {Summary: "Summary A", Boundary: &session.SummaryBoundary{Version: 1}}}
	right := map[string]*session.Summary{"": {Summary: "Summary B", Boundary: &session.SummaryBoundary{Version: 1}}}
	diffs := c.compareSummaries(left, right, "$.summaries")
	hasTextDiff := false
	for _, d := range diffs {
		if d.Path == "$.summaries..summary" {
			hasTextDiff = true
		}
	}
	if !hasTextDiff {
		t.Error("should detect summary text mismatch")
	}
}

func TestCompareSummaries_LossDetection(t *testing.T) {
	c := NewComparator(nil)
	left := map[string]*session.Summary{"": {Summary: "Present"}}
	right := map[string]*session.Summary{}
	diffs := c.compareSummaries(left, right, "$.summaries")
	if len(diffs) == 0 {
		t.Fatal("summary loss should be detected")
	}
	if diffs[0].Severity != SeverityError {
		t.Error("summary loss should be error severity")
	}
	t.Logf("summary loss: %s", diffs[0].Message)
}

func TestCompareSummaries_FilterKeyError(t *testing.T) {
	c := NewComparator(nil)
	left := map[string]*session.Summary{
		"branch-a": {Summary: "text", Boundary: &session.SummaryBoundary{Version: 1, FilterKey: "branch-a"}},
	}
	right := map[string]*session.Summary{
		"branch-a": {Summary: "text", Boundary: &session.SummaryBoundary{Version: 1, FilterKey: "branch-b"}},
	}
	diffs := c.compareSummaries(left, right, "$.summaries")
	hasFKErr := false
	for _, d := range diffs {
		if d.Path == "$.summaries.branch-a.boundary.filterKey" {
			hasFKErr = true
		}
	}
	if !hasFKErr {
		t.Error("should detect filterKey boundary error")
	}
}

func TestCompareSummaries_BoundaryMissing(t *testing.T) {
	c := NewComparator(nil)
	left := map[string]*session.Summary{"": {Summary: "x", Boundary: &session.SummaryBoundary{Version: 1}}}
	right := map[string]*session.Summary{"": {Summary: "x"}}
	diffs := c.compareSummaries(left, right, "$.summaries")
	hasBoundaryDiff := false
	for _, d := range diffs {
		if d.Path == "$.summaries..boundary" {
			hasBoundaryDiff = true
		}
	}
	if !hasBoundaryDiff {
		t.Error("should detect boundary presence mismatch")
	}
}

func TestCompareSummaries_TopicsDiffer(t *testing.T) {
	c := NewComparator(nil)
	left := map[string]*session.Summary{"": {Summary: "x", Topics: []string{"a", "b"}}}
	right := map[string]*session.Summary{"": {Summary: "x", Topics: []string{"b", "c"}}}
	diffs := c.compareSummaries(left, right, "$.summaries")
	// Should detect topic element mismatch.
	if len(diffs) == 0 {
		t.Error("should detect topics difference")
	}
}

// Comparator.compareTracks

func TestCompareTracks_TrackMissingInRight(t *testing.T) {
	c := NewComparator(nil)
	left := map[session.Track]*session.TrackEvents{"log": {Track: "log", Events: []session.TrackEvent{{}}}}
	right := map[session.Track]*session.TrackEvents{}
	diffs := c.compareTracks(left, right, "$.tracks")
	if len(diffs) == 0 {
		t.Fatal("should detect missing track")
	}
}

func TestCompareTracks_PayloadMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := map[session.Track]*session.TrackEvents{
		"t": {Track: "t", Events: []session.TrackEvent{{Payload: json.RawMessage(`{"v":1}`)}}},
	}
	right := map[session.Track]*session.TrackEvents{
		"t": {Track: "t", Events: []session.TrackEvent{{Payload: json.RawMessage(`{"v":2}`)}}},
	}
	diffs := c.compareTracks(left, right, "$.tracks")
	if len(diffs) == 0 {
		t.Fatal("should detect payload mismatch")
	}
}

func TestCompareTracks_EventCountMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := map[session.Track]*session.TrackEvents{
		"t": {Track: "t", Events: []session.TrackEvent{{}, {}}},
	}
	right := map[session.Track]*session.TrackEvents{
		"t": {Track: "t", Events: []session.TrackEvent{{}}},
	}
	diffs := c.compareTracks(left, right, "$.tracks")
	if len(diffs) == 0 {
		t.Fatal("should detect track event count mismatch")
	}
}

// Comparator.CompareMemories

func TestCompareMemories_ContentMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "Left content"}}}
	right := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "Right content"}}}
	diffs := c.CompareMemories(left, right, "$.memories")
	if len(diffs) == 0 {
		t.Fatal("should detect content mismatch")
	}
}

func TestCompareMemories_MissingInRight(t *testing.T) {
	c := NewComparator(nil)
	left := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "x"}}}
	right := []*memory.Entry{}
	diffs := c.CompareMemories(left, right, "$.memories")
	if len(diffs) == 0 {
		t.Fatal("should detect missing memory entry")
	}
}

func TestCompareMemories_KindMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "x", Kind: memory.KindFact}}}
	right := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "x", Kind: memory.KindEpisode}}}
	diffs := c.CompareMemories(left, right, "$.memories")
	hasKindDiff := false
	for _, d := range diffs {
		if d.Path == "$.memories.[m1].memory.kind" {
			hasKindDiff = true
		}
	}
	if !hasKindDiff {
		t.Error("should detect kind mismatch")
	}
}

func TestCompareMemories_TopicsOrderIndependent(t *testing.T) {
	c := NewComparator(nil)
	left := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "x", Topics: []string{"a", "b"}}}}
	right := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "x", Topics: []string{"b", "a"}}}}
	diffs := c.CompareMemories(left, right, "$.memories")
	// Topics with same elements but different order should pass (sorted before comparison).
	topicDiffs := 0
	for _, d := range diffs {
		if d.Path == "$.memories.[m1].memory.topics[0]" || d.Path == "$.memories.[m1].memory.topics[1]" {
			topicDiffs++
		}
	}
	if topicDiffs > 0 {
		t.Error("topics order should not cause diffs (sorted comparison)")
	}
}

func TestCompareMemories_ScoreFloatTolerance(t *testing.T) {
	c := NewComparator(nil)
	left := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "x"}, Score: 0.85230}}
	right := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "x"}, Score: 0.85235}}
	diffs := c.CompareMemories(left, right, "$.memories")
	// floatEqual with epsilon 1e-4 — 0.00005 > 0.0001? No, 0.00005 < 0.0001.
	if len(diffs) != 0 {
		t.Error("small score diff within epsilon should be ignored")
	}
}

func TestCompareMemories_ScoreLargeDiff(t *testing.T) {
	c := NewComparator(nil)
	left := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "x"}, Score: 0.85}}
	right := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "x"}, Score: 0.10}}
	diffs := c.CompareMemories(left, right, "$.memories")
	hasScoreDiff := false
	for _, d := range diffs {
		if d.Path == "$.memories.[m1].score" {
			hasScoreDiff = true
		}
	}
	if !hasScoreDiff {
		t.Error("large score diff should be detected")
	}
}

func TestCompareMemories_ParticipantsMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "x", Participants: []string{"Alice"}}}}
	right := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "x", Participants: []string{"Bob"}}}}
	diffs := c.CompareMemories(left, right, "$.memories")
	if len(diffs) == 0 {
		t.Fatal("should detect participants mismatch")
	}
}

// Comparator.compareExtensions
func TestCompareExtensions_PresenceMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := map[string]json.RawMessage{"ext1": json.RawMessage(`"v1"`)}
	right := map[string]json.RawMessage{}
	diffs := c.compareExtensions(left, right, "$.ext")
	if len(diffs) == 0 {
		t.Fatal("should detect extension presence mismatch")
	}
}

func TestCompareExtensions_ContentMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := map[string]json.RawMessage{"ext1": json.RawMessage(`{"v":1}`)}
	right := map[string]json.RawMessage{"ext1": json.RawMessage(`{"v":2}`)}
	diffs := c.compareExtensions(left, right, "$.ext")
	if len(diffs) == 0 {
		t.Fatal("should detect extension content mismatch")
	}
}

func TestCompareExtensions_OrderNormalized(t *testing.T) {
	c := NewComparator(nil)
	// ext value JSON fields in different order but semantically equal.
	left := map[string]json.RawMessage{"ext1": json.RawMessage(`{"a":1,"b":2}`)}
	right := map[string]json.RawMessage{"ext1": json.RawMessage(`{"b":2,"a":1}`)}
	diffs := c.compareExtensions(left, right, "$.ext")
	if len(diffs) != 0 {
		t.Errorf("order-different JSON should be equal after normalization, got %d diffs", len(diffs))
	}
}

// Comparator.filterAllowed
func TestFilterAllowed_RemovesIgnoredPath(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.events[*].id", Kind: "auto_id", Strategy: "ignore"},
	})
	diffs := []DiffResult{
		{Path: "$.events[0].id", Kind: DiffValueMismatch, Severity: SeverityError},
		{Path: "$.events[0].author", Kind: DiffValueMismatch, Severity: SeverityError},
	}
	filtered := c.filterAllowed(diffs, "a", "b")
	if len(filtered) != 1 {
		t.Fatalf("expected 1 diff after filter, got %d", len(filtered))
	}
	if filtered[0].Path != "$.events[0].author" {
		t.Error("author diff should survive filter")
	}
}

func TestFilterAllowed_BackendScopedRule(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.events[*].id", Kind: "auto_id", Strategy: "ignore", Backends: []string{"sqlite"}},
	})
	diffs := []DiffResult{
		{Path: "$.events[0].id", Kind: DiffValueMismatch, Severity: SeverityError},
	}
	// Compare with sqlite as compared backend — should be filtered.
	filtered := c.filterAllowed(diffs, "inmemory", "sqlite")
	if len(filtered) != 0 {
		t.Error("rule with Backends=[sqlite] should match sqlite as compared")
	}
	// Compare with two non-matching backends — should NOT be filtered.
	filtered = c.filterAllowed(diffs, "inmemory", "postgres")
	if len(filtered) == 0 {
		t.Error("rule with Backends=[sqlite] should NOT match postgres")
	}
}

// DiffRule.MatchPath

func TestDiffRule_MatchPath_Exact(t *testing.T) {
	r := DiffRule{Path: "$.events[0].author"}
	if !r.MatchPath("$.events[0].author") {
		t.Error("exact match should work")
	}
	if r.MatchPath("$.events[1].author") {
		t.Error("exact should not match different index")
	}
}

func TestDiffRule_MatchPath_WildcardIndex(t *testing.T) {
	r := DiffRule{Path: "$.events[*].id"}
	if !r.MatchPath("$.events[5].id") {
		t.Error("[*] should match any index")
	}
}

func TestDiffRule_MatchPath_TerminalWildcard(t *testing.T) {
	r := DiffRule{Path: "$.state[*]"}
	if !r.MatchPath("$.state.app:version") {
		t.Error("terminal [*] should match any key")
	}
}

func TestDiffRule_MatchPath_MultiWildcard(t *testing.T) {
	r := DiffRule{Path: "$.tracks[*].events[*].timestamp"}
	if !r.MatchPath("$.tracks.tool_exec.events[3].timestamp") {
		t.Error("double [*] should match track name and event index")
	}
}

// CompareJSON — general deep comparison
func TestCompareJSON_EqualValues(t *testing.T) {
	diffs := CompareJSON(map[string]any{"a": 1}, map[string]any{"a": 1}, "$")
	if len(diffs) != 0 {
		t.Errorf("equal values should have 0 diffs, got %d", len(diffs))
	}
}

func TestCompareJSON_NestedMap(t *testing.T) {
	left := map[string]any{"outer": map[string]any{"inner": "value"}}
	right := map[string]any{"outer": map[string]any{"inner": "different"}}
	diffs := CompareJSON(left, right, "$")
	if len(diffs) == 0 {
		t.Error("should detect nested map value diff")
	}
}

func TestCompareJSON_ExtraKey(t *testing.T) {
	left := map[string]any{"a": 1, "b": 2}
	right := map[string]any{"a": 1}
	diffs := CompareJSON(left, right, "$")
	// "b" is in left but not right → DiffMissingKey (missing in right).
	hasKeyDiff := false
	for _, d := range diffs {
		if d.Kind == DiffMissingKey || d.Kind == DiffExtraKey {
			hasKeyDiff = true
		}
	}
	if !hasKeyDiff {
		t.Error("should detect key difference")
	}
}

func TestCompareJSON_SliceLengthMismatch(t *testing.T) {
	left := []any{1, 2, 3}
	right := []any{1, 2}
	diffs := CompareJSON(left, right, "$")
	if len(diffs) == 0 {
		t.Error("should detect slice length mismatch")
	}
}

// helpers
// DiffRule.MatchPath — segment boundaries.

func TestDiffRule_MatchPath_SegmentBoundary(t *testing.T) {
	r := DiffRule{Path: "$.state[*]"}
	if !r.MatchPath("$.state.app:version") {
		t.Error("$.state[*] should match $.state.app:version (single segment)")
	}
	if r.MatchPath("$.state.a.b") {
		t.Error("$.state[*] should NOT match $.state.a.b (crosses segment boundary)")
	}
	if r.MatchPath("$.state.a.b.c") {
		t.Error("$.state[*] should NOT match $.state.a.b.c")
	}
}

func TestDiffRule_MatchPath_MemoriesWildcard(t *testing.T) {
	r := DiffRule{Path: "$.memories[*].memory.eventTime"}
	if !r.MatchPath("$.memories.[mem-1].memory.eventTime") {
		t.Error("$.memories[*].memory.eventTime should match comparator output path")
	}
}

// tokenizePath.

func TestTokenizePath_Basic(t *testing.T) {
	got := tokenizePath("$.events[3].id")
	want := []string{"$", "events", "3", "id"}
	assertSegs(t, got, want)
}

func TestTokenizePath_BracketedKey(t *testing.T) {
	got := tokenizePath("$.memories.[mem-1].memory.memory")
	want := []string{"$", "memories", "mem-1", "memory", "memory"}
	assertSegs(t, got, want)
}

func TestTokenizePath_QuotedKey(t *testing.T) {
	got := tokenizePath("$.memories.[\"a-b\"].score")
	want := []string{"$", "memories", "a-b", "score"}
	assertSegs(t, got, want)
}

func TestTokenizePath_Wildcard(t *testing.T) {
	got := tokenizePath("$.events[*].timestamp")
	want := []string{"$", "events", "*", "timestamp"}
	assertSegs(t, got, want)
}

// CompareJSON — pointer safety.

func TestCompareJSON_LeftNilPtr(t *testing.T) {
	var x *int
	y := 5
	// Should not panic.
	diffs := CompareJSON(x, y, "$")
	if len(diffs) == 0 {
		t.Error("expected diff for nil pointer vs int")
	}
}

func TestCompareJSON_RightNilPtr(t *testing.T) {
	x := 5
	var y *int
	diffs := CompareJSON(x, y, "$")
	if len(diffs) == 0 {
		t.Error("expected diff for int vs nil pointer")
	}
}

func TestCompareJSON_BothNilPtr(t *testing.T) {
	var x *int
	var y *int
	diffs := CompareJSON(x, y, "$")
	if len(diffs) != 0 {
		t.Errorf("both nil pointers should have 0 diffs, got %d", len(diffs))
	}
}

func TestCompareJSON_LeftPtrRightStruct(t *testing.T) {
	x := new(int)
	*x = 42
	y := "hello"
	diffs := CompareJSON(x, y, "$")
	if len(diffs) == 0 {
		t.Error("expected type_mismatch for *int vs string")
	}
}

func assertSegs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("at [%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func makeEvent(author, content, branch, tag, filterKey string) event.Event {
	return event.Event{
		Author:    author,
		Branch:    branch,
		Tag:       tag,
		FilterKey: filterKey,
		Version:   event.CurrentVersion,
		Response: &model.Response{
			ID: "resp-test",
			Choices: []model.Choice{{
				Index:   0,
				Message: model.Message{Role: model.RoleUser, Content: content},
			}},
		},
	}
}

func makeEventWithResp(author, content, branch, tag, filterKey string, resp *model.Response) event.Event {
	ev := makeEvent(author, content, branch, tag, filterKey)
	if resp != nil {
		ev.Response = resp
	}
	return ev
}

// --- compareUsage ---

func TestCompareUsage_TokenMismatches(t *testing.T) {
	c := NewComparator(nil)
	left := &model.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}
	right := &model.Usage{PromptTokens: 200, CompletionTokens: 100, TotalTokens: 300}
	diffs := c.compareUsage(left, right, "$.usage")

	if len(diffs) != 3 {
		t.Fatalf("expected 3 diffs, got %d", len(diffs))
	}
	paths := map[string]bool{}
	for _, d := range diffs {
		paths[d.Path] = true
	}
	for _, p := range []string{"$.usage.promptTokens", "$.usage.completionTokens", "$.usage.totalTokens"} {
		if !paths[p] {
			t.Errorf("missing diff for %s", p)
		}
	}
}

func TestCompareUsage_PresenceMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := &event.Event{Author: "user", Response: &model.Response{
		Object:  "chat.completion",
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser}}},
		Usage:   &model.Usage{PromptTokens: 10},
	}}
	right := &event.Event{Author: "user", Response: &model.Response{
		Object:  "chat.completion",
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser}}},
	}}
	diffs := c.compareEvent(left, right, "$.events[0]")

	hasUsage := false
	for _, d := range diffs {
		if d.Path == "$.events[0].response.usage" {
			hasUsage = true
		}
	}
	if !hasUsage {
		t.Error("should detect usage presence mismatch")
	}
}

// --- compareResponses ---

func TestCompareResponses_ObjectMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := &event.Event{Author: "user", Response: &model.Response{Object: "chat.completion", Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hi"}}}}}
	right := &event.Event{Author: "user", Response: &model.Response{Object: "chat.completion.chunk", Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hi"}}}}}
	diffs := c.compareEvent(left, right, "$.events[0]")

	assertHasDiffPath(t, diffs, "$.events[0].response.object")
}

func TestCompareResponses_ModelNameMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := &event.Event{Author: "user", Response: &model.Response{Model: "gpt-4", Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hi"}}}}}
	right := &event.Event{Author: "user", Response: &model.Response{Model: "gpt-3.5", Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hi"}}}}}
	diffs := c.compareEvent(left, right, "$.events[0]")

	assertHasDiffPath(t, diffs, "$.events[0].response.model")
}

func TestCompareResponses_ChoicesCountMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := &event.Event{Author: "user", Response: &model.Response{Choices: []model.Choice{
		{Message: model.Message{Role: model.RoleUser, Content: "a"}},
		{Message: model.Message{Role: model.RoleUser, Content: "b"}},
	}}}
	right := &event.Event{Author: "user", Response: &model.Response{Choices: []model.Choice{
		{Message: model.Message{Role: model.RoleUser, Content: "a"}},
	}}}
	diffs := c.compareEvent(left, right, "$.events[0]")

	assertHasDiffPath(t, diffs, "$.events[0].response.choices")
}

// --- compareMessage: toolID / toolName ---

func TestCompareMessage_ToolIDMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := &event.Event{Author: "tool", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleTool, Content: "result", ToolID: "t1", ToolName: "search",
	}}}}}
	right := &event.Event{Author: "tool", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleTool, Content: "result", ToolID: "t2", ToolName: "search",
	}}}}}
	diffs := c.compareEvent(left, right, "$.events[0]")

	assertHasDiffPath(t, diffs, "$.events[0].response.choices[0].message.toolID")
}

func TestCompareMessage_ToolNameMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := &event.Event{Author: "tool", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleTool, Content: "result", ToolID: "t1", ToolName: "search",
	}}}}}
	right := &event.Event{Author: "tool", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleTool, Content: "result", ToolID: "t1", ToolName: "fetch",
	}}}}}
	diffs := c.compareEvent(left, right, "$.events[0]")

	assertHasDiffPath(t, diffs, "$.events[0].response.choices[0].message.toolName")
}

func TestCompareMessage_ToolCallsCountMismatch(t *testing.T) {
	c := NewComparator(nil)
	left := &event.Event{Author: "assistant", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{ID: "tc1", Type: "function", Function: model.FunctionDefinitionParam{Name: "search"}},
			{ID: "tc2", Type: "function", Function: model.FunctionDefinitionParam{Name: "fetch"}},
		},
	}}}}}
	right := &event.Event{Author: "assistant", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{ID: "tc1", Type: "function", Function: model.FunctionDefinitionParam{Name: "search"}},
		},
	}}}}}
	diffs := c.compareEvent(left, right, "$.events[0]")

	assertHasDiffPath(t, diffs, "$.events[0].response.choices[0].message.toolCalls")
}

// --- compareMemoryEntry: nil memory content ---

func TestCompareMemoryEntry_NilMemoryContent(t *testing.T) {
	c := NewComparator(nil)
	left := []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "present"}}}
	right := []*memory.Entry{{ID: "m1", Memory: nil}}
	diffs := c.CompareMemories(left, right, "$.memories")

	assertHasDiffPath(t, diffs, "$.memories.[m1].memory")
}

// --- isAllowed: strategy coverage ---

// TestComparator_isAllowed_ExtraKeysOnlyMatchesCorrectKind verifies that allow_extra_keys matches when d.Kind == DiffExtraKey but NOT for other kinds.
func TestComparator_isAllowed_ExtraKeysOnlyMatchesCorrectKind(t *testing.T) {
	c := NewComparator([]DiffRule{
		{Path: "$.state[*]", Kind: "backend_metadata", Strategy: "allow_extra_keys"},
	})
	// DiffResult has no Kind set → zero-value "", which does NOT match DiffExtraKey.
	_, ok := c.isAllowed(&DiffResult{Path: "$.state.extra_field"})
	if ok {
		t.Error("allow_extra_keys should not match when d.Kind is not DiffExtraKey")
	}
}

// --- compareResponses: systemFingerprint ---

func TestCompareResponses_SystemFingerprintMismatch(t *testing.T) {
	c := NewComparator(nil)
	fp1 := "fp_abc"
	fp2 := "fp_xyz"
	left := &event.Event{Author: "assistant", Response: &model.Response{
		Choices:           []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "ok"}}},
		SystemFingerprint: &fp1,
	}}
	right := &event.Event{Author: "assistant", Response: &model.Response{
		Choices:           []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "ok"}}},
		SystemFingerprint: &fp2,
	}}
	diffs := c.compareEvent(left, right, "$.events[0]")

	assertHasDiffPath(t, diffs, "$.events[0].response.systemFingerprint")
}

func TestCompareResponses_SystemFingerprintOneNil(t *testing.T) {
	c := NewComparator(nil)
	fp1 := "fp_abc"
	left := &event.Event{Author: "assistant", Response: &model.Response{
		Choices:           []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "ok"}}},
		SystemFingerprint: &fp1,
	}}
	right := &event.Event{Author: "assistant", Response: &model.Response{
		Choices:           []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "ok"}}},
		SystemFingerprint: nil,
	}}
	diffs := c.compareEvent(left, right, "$.events[0]")

	// One side has nil systemFingerprint — now produces a presence-mismatch diff so the default DiffRule can document the intentional allowance.
	assertHasDiffPath(t, diffs, "$.events[0].response.systemFingerprint")
}

// --- helpers ---

func assertHasDiffPath(t *testing.T, diffs []DiffResult, path string) {
	t.Helper()
	for _, d := range diffs {
		if d.Path == path {
			return
		}
	}
	t.Errorf("expected diff at path %s, but not found. got %d diffs:", path, len(diffs))
	for _, d := range diffs {
		t.Logf("  %s | %s | %s", d.Path, d.Kind, d.Message)
	}
}
