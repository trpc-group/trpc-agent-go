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
	"encoding/json"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestComparator_EqualResults(t *testing.T) {
	c := NewComparator()
	now := time.Now().UTC()

	a := &BackendResult{
		BackendName: "A",
		Session: &session.Session{
			AppName:   "test",
			UserID:    "u1",
			Events:    nil,
			State:     session.StateMap{"k": []byte("v")},
			CreatedAt: now,
		},
	}
	b := &BackendResult{
		BackendName: "B",
		Session: &session.Session{
			AppName:   "test",
			UserID:    "u1",
			Events:    nil,
			State:     session.StateMap{"k": []byte("v")},
			CreatedAt: now,
		},
	}

	diffs := c.Compare("case1", a, b)
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs for equal results, got %d: %v", len(diffs), diffs)
	}
}

func TestComparator_DifferentStrings(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}

	diffs := c.compareString("case", a, b, "field", "hello", "world")
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].Baseline != "hello" || diffs[0].Actual != "world" {
		t.Errorf("expected baseline=hello actual=world, got baseline=%v actual=%v", diffs[0].Baseline, diffs[0].Actual)
	}
}

func TestComparator_TimeTolerance(t *testing.T) {
	c := NewComparator()
	now := time.Now().UTC()

	// Within 1s: no diff.
	diffs := c.compareTime("case", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"},
		"ts", now, now.Add(500*time.Millisecond))
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs for 500ms delta, got %d", len(diffs))
	}

	// Beyond 1s: diff.
	diffs = c.compareTime("case", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"},
		"ts", now, now.Add(2*time.Second))
	if len(diffs) != 1 {
		t.Errorf("expected 1 diff for 2s delta, got %d", len(diffs))
	}
}

func TestComparator_FloatTolerance(t *testing.T) {
	c := NewComparator()

	diffs := c.compareFloat("case", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"},
		"score", 0.5, 0.505)
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs for 0.005 delta, got %d", len(diffs))
	}

	diffs = c.compareFloat("case", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"},
		"score", 0.5, 0.6)
	if len(diffs) != 1 {
		t.Errorf("expected 1 diff for 0.1 delta, got %d", len(diffs))
	}
}

func TestComparator_NilResults(t *testing.T) {
	c := NewComparator()
	diffs := c.Compare("case", nil, nil)
	if len(diffs) != 1 {
		t.Errorf("expected 1 diff for nil results, got %d", len(diffs))
	}
}

func TestComparator_StateMismatch(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{
		BackendName: "A",
		Session: &session.Session{
			State: session.StateMap{"k": []byte("v1")},
		},
	}
	b := &BackendResult{
		BackendName: "B",
		Session: &session.Session{
			State: session.StateMap{"k": []byte("v2")},
		},
	}
	diffs := c.Compare("case", a, b)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "session.state.k" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at session.state.k")
	}
}

func TestComparator_TrackMismatch(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{
		BackendName: "A",
		Session: &session.Session{
			Tracks: map[session.Track]*session.TrackEvents{
				"tool_exec": {Track: "tool_exec", Events: []session.TrackEvent{
					{Track: "tool_exec", Timestamp: time.Now().UTC()},
				}},
			},
		},
	}
	b := &BackendResult{
		BackendName: "B",
		Session: &session.Session{
			Tracks: map[session.Track]*session.TrackEvents{
				"tool_exec": {Track: "tool_exec", Events: []session.TrackEvent{
					{Track: "tool_exec", Timestamp: time.Now().UTC()},
				}},
			},
		},
	}
	diffs := c.Compare("case", a, b)
	for _, d := range diffs {
		if !d.AllowedDiff {
			t.Errorf("unexpected unallowed diff: %s (%s)", d.FieldPath, d.DiffReason)
		}
	}
}

// --- compareInt ---

func TestComparator_compareInt_Equal(t *testing.T) {
	c := NewComparator()
	diffs := c.compareInt("c", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"}, "f", 42, 42)
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs for equal ints, got %d", len(diffs))
	}
}

func TestComparator_compareInt_Diff(t *testing.T) {
	c := NewComparator()
	diffs := c.compareInt("c", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"}, "f", 42, 99)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].FieldPath != "f" {
		t.Errorf("expected field path 'f', got %q", diffs[0].FieldPath)
	}
}

// --- compareTimePtr ---

func TestComparator_compareTimePtr_BothNil(t *testing.T) {
	c := NewComparator()
	diffs := c.compareTimePtr("c", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"}, "f", nil, nil)
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs for both nil, got %d", len(diffs))
	}
}

func TestComparator_compareTimePtr_OneNil(t *testing.T) {
	c := NewComparator()
	now := time.Now().UTC()
	diffs := c.compareTimePtr("c", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"}, "f", &now, nil)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].DiffReason != "time pointer is nil on one side" {
		t.Errorf("unexpected reason: %q", diffs[0].DiffReason)
	}
}

func TestComparator_compareTimePtr_BothSetWithinTolerance(t *testing.T) {
	c := NewComparator()
	now := time.Now().UTC()
	tA := now
	tB := now.Add(500 * time.Millisecond)
	diffs := c.compareTimePtr("c", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"}, "f", &tA, &tB)
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs within tolerance, got %d", len(diffs))
	}
}

func TestComparator_compareTimePtr_BothSetBeyondTolerance(t *testing.T) {
	c := NewComparator()
	now := time.Now().UTC()
	tA := now
	tB := now.Add(2 * time.Second)
	diffs := c.compareTimePtr("c", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"}, "f", &tA, &tB)
	if len(diffs) == 0 {
		t.Error("expected diffs beyond tolerance")
	}
}

// --- compareEventLists ---

func TestComparator_CompareEventLists_LengthMismatch(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}

	// Different lengths.
	eventsA := []event.Event{{Author: "user"}}
	eventsB := []event.Event{{Author: "user"}, {Author: "assistant"}}

	diffs := c.compareEventLists("c", a, b, eventsA, eventsB)
	// Should have a length mismatch diff + 1 event comparison.
	if len(diffs) < 1 {
		t.Fatal("expected at least 1 diff for length mismatch")
	}
	found := false
	for _, d := range diffs {
		if d.FieldPath == "events" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'events' field path in diffs")
	}
}

// --- compareModelToolCalls ---

func TestComparator_CompareModelToolCalls_CountMismatch(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}

	tcA := []model.ToolCall{{ID: "call_1", Function: model.FunctionDefinitionParam{Name: "foo"}}}
	tcB := []model.ToolCall{tcA[0], {ID: "call_2", Function: model.FunctionDefinitionParam{Name: "bar"}}}

	diffs := c.compareModelToolCalls("c", a, b, "tc", tcA, tcB)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff for count mismatch, got %d", len(diffs))
	}
	if diffs[0].DiffReason != "tool call count mismatch: 1 vs 2" {
		t.Errorf("unexpected reason: %q", diffs[0].DiffReason)
	}
}

func TestComparator_CompareModelToolCalls_ArgsDiff(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}

	tcA := []model.ToolCall{{
		ID:   "call_1",
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      "get_weather",
			Arguments: json.RawMessage(`{"city":"London"}`),
		},
	}}
	tcB := []model.ToolCall{{
		ID:   "call_1",
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      "get_weather",
			Arguments: json.RawMessage(`{"city":"Paris"}`),
		},
	}}

	diffs := c.compareModelToolCalls("c", a, b, "tc", tcA, tcB)
	// Should find args differ.
	found := false
	for _, d := range diffs {
		if d.FieldPath == "tc[0].function.arguments" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at tc[0].function.arguments")
	}
}

// --- compareToolResponse ---

func TestComparator_CompareToolResponse_BothNil(t *testing.T) {
	c := NewComparator()
	diffs := c.compareToolResponse("c", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"}, "tr", nil, nil)
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs for both nil, got %d", len(diffs))
	}
}

func TestComparator_CompareToolResponse_OneNil(t *testing.T) {
	c := NewComparator()
	tr := &model.Message{Content: "result", Role: model.RoleTool, ToolID: "call_1"}
	diffs := c.compareToolResponse("c", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"}, "tr", tr, nil)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff for one nil, got %d", len(diffs))
	}
	if diffs[0].DiffReason != "tool response message is nil on one side" {
		t.Errorf("unexpected reason: %q", diffs[0].DiffReason)
	}
}

func TestComparator_CompareToolResponse_ContentDiff(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	trA := &model.Message{Content: "result A", Role: model.RoleTool, ToolID: "call_1", ToolName: "get_weather"}
	trB := &model.Message{Content: "result B", Role: model.RoleTool, ToolID: "call_1", ToolName: "get_weather"}
	diffs := c.compareToolResponse("c", a, b, "tr", trA, trB)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "tr.content" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at tr.content")
	}
}

// --- compareStateMaps ---

func TestComparator_CompareStateMaps_SizeMismatch(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	smA := session.StateMap{"k1": []byte("v1")}
	smB := session.StateMap{"k1": []byte("v1"), "k2": []byte("v2")}
	diffs := c.compareStateMaps("c", a, b, "st", smA, smB)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "st" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at 'st' for size mismatch")
	}
}

func TestComparator_CompareStateMaps_KeyOneSide(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	smA := session.StateMap{"k1": []byte("v1")}
	smB := session.StateMap{"k2": []byte("v2")}
	diffs := c.compareStateMaps("c", a, b, "st", smA, smB)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "st.k1" || d.FieldPath == "st.k2" {
			if d.DiffReason == "key exists in one side only (A:true B:false)" ||
				d.DiffReason == "key exists in one side only (A:false B:true)" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("expected diff for key-exists-one-side")
	}
}

// --- compareSummaryBoundary ---

func TestComparator_CompareSummaryBoundary_BothNil(t *testing.T) {
	c := NewComparator()
	diffs := c.compareSummaryBoundary("c", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"}, "b", nil, nil)
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs, got %d", len(diffs))
	}
}

func TestComparator_CompareSummaryBoundary_OneNil(t *testing.T) {
	c := NewComparator()
	sb := &session.SummaryBoundary{Version: 1}
	diffs := c.compareSummaryBoundary("c", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"}, "b", sb, nil)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff for one nil, got %d", len(diffs))
	}
	if diffs[0].DiffReason != "summary boundary is nil on one side" {
		t.Errorf("unexpected reason: %q", diffs[0].DiffReason)
	}
}

func TestComparator_CompareSummaryBoundary_VersionDiff(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	sbA := &session.SummaryBoundary{Version: 1}
	sbB := &session.SummaryBoundary{Version: 2}
	diffs := c.compareSummaryBoundary("c", a, b, "b", sbA, sbB)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "b.version" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at b.version")
	}
}

func TestComparator_CompareSummaryBoundary_CutoffAtDiff(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	now := time.Now().UTC()
	sbA := &session.SummaryBoundary{CutoffAt: now}
	sbB := &session.SummaryBoundary{CutoffAt: now.Add(2 * time.Second)}
	diffs := c.compareSummaryBoundary("c", a, b, "b", sbA, sbB)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "b.cutoffAt" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at b.cutoffAt")
	}
}

func TestComparator_CompareSummaryBoundary_LastEventIDDiff(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	sbA := &session.SummaryBoundary{LastEventID: "evt-1"}
	sbB := &session.SummaryBoundary{LastEventID: "evt-2"}
	diffs := c.compareSummaryBoundary("c", a, b, "b", sbA, sbB)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "b.lastEventID" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at b.lastEventID")
	}
}

// --- compareMemories ---

func TestComparator_CompareMemories_CountMismatch(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	a.Memories = []*memory.Entry{
		{Memory: &memory.Memory{Memory: "mem1", Kind: "fact"}},
	}
	b.Memories = []*memory.Entry{
		{Memory: &memory.Memory{Memory: "mem1", Kind: "fact"}},
		{Memory: &memory.Memory{Memory: "mem2", Kind: "episode"}},
	}
	diffs := c.compareMemories("c", a, b)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "memories" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at 'memories' for count mismatch")
	}
}

func TestComparator_CompareMemories_NilEntry(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	a.Memories = []*memory.Entry{nil}
	b.Memories = []*memory.Entry{{Memory: &memory.Memory{Memory: "mem1", Kind: "fact"}}}
	diffs := c.compareMemories("c", a, b)
	// The nil entry diff is only detected if both non-nil entries are sorted identically.
	if len(diffs) < 1 {
		t.Error("expected at least 1 diff for nil entry")
	}
}

// --- compareMemoryContent ---

func TestComparator_CompareMemoryContent_BothNil(t *testing.T) {
	c := NewComparator()
	diffs := c.compareMemoryContent("c", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"}, "m", nil, nil)
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs, got %d", len(diffs))
	}
}

func TestComparator_CompareMemoryContent_OneNil(t *testing.T) {
	c := NewComparator()
	m := &memory.Memory{Memory: "content", Kind: "fact"}
	diffs := c.compareMemoryContent("c", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"}, "m", m, nil)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff for one nil, got %d", len(diffs))
	}
	if diffs[0].DiffReason != "memory content is nil on one side" {
		t.Errorf("unexpected reason: %q", diffs[0].DiffReason)
	}
}

func TestComparator_CompareMemoryContent_TopicsDiff(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	mA := &memory.Memory{Memory: "content", Kind: "fact", Topics: []string{"a", "b"}}
	mB := &memory.Memory{Memory: "content", Kind: "fact", Topics: []string{"a", "c"}}
	diffs := c.compareMemoryContent("c", a, b, "m", mA, mB)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "m.topics" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at m.topics")
	}
}

func TestComparator_CompareMemoryContent_KindDiff(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	mA := &memory.Memory{Memory: "content", Kind: "fact"}
	mB := &memory.Memory{Memory: "content", Kind: "episode"}
	diffs := c.compareMemoryContent("c", a, b, "m", mA, mB)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "m.kind" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at m.kind")
	}
}

func TestComparator_CompareMemoryContent_ParticipantsDiff(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	mA := &memory.Memory{Memory: "content", Kind: "episode", Participants: []string{"Alice"}}
	mB := &memory.Memory{Memory: "content", Kind: "episode", Participants: []string{"Bob"}}
	diffs := c.compareMemoryContent("c", a, b, "m", mA, mB)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "m.participants" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at m.participants")
	}
}

func TestComparator_CompareMemoryContent_LocationDiff(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	mA := &memory.Memory{Memory: "content", Kind: "fact", Location: "NYC"}
	mB := &memory.Memory{Memory: "content", Kind: "fact", Location: "LA"}
	diffs := c.compareMemoryContent("c", a, b, "m", mA, mB)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "m.location" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at m.location")
	}
}

func TestComparator_CompareMemoryContent_EventTimeDiff(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	now := time.Now().UTC()
	mA := &memory.Memory{Memory: "content", Kind: "episode", EventTime: &now}
	future := now.Add(2 * time.Second)
	mB := &memory.Memory{Memory: "content", Kind: "episode", EventTime: &future}
	diffs := c.compareMemoryContent("c", a, b, "m", mA, mB)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "m.eventTime" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at m.eventTime")
	}
}

// --- compareSummaryTexts ---

func TestComparator_CompareSummaryTexts_KeyOneSide(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A", SummaryTexts: map[string]string{"k1": "text1"}}
	b := &BackendResult{BackendName: "B", SummaryTexts: map[string]string{"k2": "text2"}}
	diffs := c.compareSummaryTexts("c", a, b)
	found := false
	for _, d := range diffs {
		if d.SummaryKey == "k1" || d.SummaryKey == "k2" {
			if d.DiffReason == "summary text exists in one side only (A:true B:false)" ||
				d.DiffReason == "summary text exists in one side only (A:false B:true)" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("expected diff for summary key exists in one side")
	}
}

func TestComparator_CompareSummaryTexts_TextDiff(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A", SummaryTexts: map[string]string{"k1": "textA"}}
	b := &BackendResult{BackendName: "B", SummaryTexts: map[string]string{"k1": "textB"}}
	diffs := c.compareSummaryTexts("c", a, b)
	found := false
	for _, d := range diffs {
		if d.SummaryKey == "k1" && d.DiffReason == "summary text differs" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff for summary text differs")
	}
}

// --- compareTracks ---

func TestComparator_CompareTracks_TrackOneSide(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A", Tracks: map[session.Track]*session.TrackEvents{
		"exec": {Track: "exec", Events: []session.TrackEvent{
			{Track: "exec", Timestamp: time.Now().UTC()},
		}},
	}}
	b := &BackendResult{BackendName: "B", Tracks: map[session.Track]*session.TrackEvents{}}
	diffs := c.compareTracks("c", a, b)
	found := false
	for _, d := range diffs {
		if d.TrackName == "exec" && d.DiffReason == "track exists in one side only (A:true B:false)" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff for track exists in one side")
	}
}

func TestComparator_CompareTracks_PayloadDiff(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A", Tracks: map[session.Track]*session.TrackEvents{
		"exec": {Track: "exec", Events: []session.TrackEvent{
			{Track: "exec", Timestamp: time.Now().UTC(), Payload: json.RawMessage(`{"result":"ok"}`)},
		}},
	}}
	b := &BackendResult{BackendName: "B", Tracks: map[session.Track]*session.TrackEvents{
		"exec": {Track: "exec", Events: []session.TrackEvent{
			{Track: "exec", Timestamp: time.Now().UTC(), Payload: json.RawMessage(`{"result":"fail"}`)},
		}},
	}}
	diffs := c.compareTracks("c", a, b)
	found := false
	for _, d := range diffs {
		if d.FieldPath == `tracks[exec].events[0].payload` {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at tracks[exec].events[0].payload")
	}
}

func TestComparator_CompareTracks_NilSide(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A", Tracks: map[session.Track]*session.TrackEvents{
		"exec": nil,
	}}
	b := &BackendResult{BackendName: "B", Tracks: map[session.Track]*session.TrackEvents{
		"exec": {Track: "exec", Events: []session.TrackEvent{
			{Track: "exec", Timestamp: time.Now().UTC()},
		}},
	}}
	diffs := c.compareTracks("c", a, b)
	found := false
	for _, d := range diffs {
		if d.TrackName == "exec" && d.DiffReason == "track events is nil on one side" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff for track events is nil on one side")
	}
}

func TestComparator_CompareTracks_EventCountMismatch(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A", Tracks: map[session.Track]*session.TrackEvents{
		"exec": {Track: "exec", Events: []session.TrackEvent{
			{Track: "exec", Timestamp: time.Now().UTC()},
		}},
	}}
	b := &BackendResult{BackendName: "B", Tracks: map[session.Track]*session.TrackEvents{
		"exec": {Track: "exec", Events: []session.TrackEvent{
			{Track: "exec", Timestamp: time.Now().UTC()},
			{Track: "exec", Timestamp: time.Now().UTC()},
		}},
	}}
	diffs := c.compareTracks("c", a, b)
	found := false
	for _, d := range diffs {
		if d.TrackName == "exec" && d.DiffReason == "track event count mismatch: 1 vs 2" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff for track event count mismatch")
	}
}

// --- compareRawMessageMaps ---

func TestComparator_CompareRawMessageMaps_KeyOneSide(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	mA := map[string]json.RawMessage{"k1": json.RawMessage(`"v1"`)}
	mB := map[string]json.RawMessage{}
	diffs := c.compareRawMessageMaps("c", a, b, "ext", mA, mB)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "ext.k1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at ext.k1 for key-one-side")
	}
}

func TestComparator_CompareRawMessageMaps_ValueDiff(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}
	mA := map[string]json.RawMessage{"k1": json.RawMessage(`"v1"`)}
	mB := map[string]json.RawMessage{"k1": json.RawMessage(`"v2"`)}
	diffs := c.compareRawMessageMaps("c", a, b, "ext", mA, mB)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "ext.k1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at ext.k1 for value diff")
	}
}

// --- compareSessions ---

func TestComparator_CompareSessions_NilOneSide(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{
		BackendName: "A",
		Session:     &session.Session{AppName: "test", UserID: "u1"},
	}
	b := &BackendResult{
		BackendName: "B",
		Session:     nil,
	}
	diffs := c.compareSessions("c", a, b)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff for nil session on one side, got %d", len(diffs))
	}
	if diffs[0].DiffReason != "session is nil on one side" {
		t.Errorf("unexpected reason: %q", diffs[0].DiffReason)
	}
}

// --- stringSliceEqual ---

func TestStringSliceEqual_Equal(t *testing.T) {
	if !stringSliceEqual([]string{"a", "b", "c"}, []string{"c", "b", "a"}) {
		t.Error("expected equal slices (order-independent)")
	}
}

func TestStringSliceEqual_DifferentLength(t *testing.T) {
	if stringSliceEqual([]string{"a", "b"}, []string{"a", "b", "c"}) {
		t.Error("expected false for different lengths")
	}
}

func TestStringSliceEqual_DifferentOrder(t *testing.T) {
	if !stringSliceEqual([]string{"a", "b"}, []string{"b", "a"}) {
		t.Error("expected true for same elements in different order")
	}
}

func TestStringSliceEqual_DifferentContent(t *testing.T) {
	if stringSliceEqual([]string{"a", "b"}, []string{"a", "c"}) {
		t.Error("expected false for different content")
	}
}

// --- backendName ---

func TestBackendName_NilResult(t *testing.T) {
	name := backendName(nil)
	if name != "<nil>" {
		t.Errorf("expected '<nil>', got %q", name)
	}
}

func TestBackendName_Normal(t *testing.T) {
	name := backendName(&BackendResult{BackendName: "InMemory"})
	if name != "InMemory" {
		t.Errorf("expected 'InMemory', got %q", name)
	}
}

// --- firstMessage ---

func TestFirstMessage_NilEvent(t *testing.T) {
	msg := firstMessage(nil)
	if msg.Content != "" {
		t.Errorf("expected empty message, got %+v", msg)
	}
}

func TestFirstMessage_NilResponse(t *testing.T) {
	msg := firstMessage(&event.Event{})
	if msg.Content != "" {
		t.Errorf("expected empty message, got %+v", msg)
	}
}
