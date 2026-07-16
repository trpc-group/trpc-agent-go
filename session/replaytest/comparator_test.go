// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestComparator_DetectsEventAndSummaryDiffs(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "single_turn_text"}
	a := &Snapshot{
		Backend:   "inmemory",
		SessionID: "s1",
		Session: &session.Session{
			Events: []event.Event{*UserEvent("e1", "hello"), *AssistantEvent("e2", "hi")},
			Summaries: map[string]*session.Summary{
				"": {Summary: "full", Topics: []string{"a"}, UpdatedAt: time.Unix(1, 0).UTC()},
			},
		},
	}
	b := &Snapshot{
		Backend:   "sqlite",
		SessionID: "s1",
		Session: &session.Session{
			Events: []event.Event{*UserEvent("e1", "hello"), *AssistantEvent("e2", "bye")},
			Summaries: map[string]*session.Summary{
				"other": {Summary: "full", Topics: []string{"a"}, UpdatedAt: time.Unix(1, 0).UTC()},
			},
		},
	}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), SQLiteProfile())
	if ErrorDiffCount(diffs) == 0 {
		t.Fatal("expected diffs")
	}
	var hasContent, hasSummary bool
	for _, d := range diffs {
		if !d.Allowed && d.Path == "events[1].response.choices[0].message.content" {
			hasContent = true
			if d.EventIndex == nil {
				t.Fatal("event_index missing")
			}
		}
		if !d.Allowed && (d.SummaryFilterKey == "" || d.SummaryFilterKey == "other") {
			if d.Path == `summaries[""]` || d.Path == `summaries["other"]` {
				hasSummary = true
			}
		}
	}
	if !hasContent {
		t.Fatalf("content diff missing: %+v", diffs)
	}
	if !hasSummary {
		t.Fatalf("summary filter-key diff missing: %+v", diffs)
	}
}

func TestComparator_AllowedDiffIgnore(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{
		Name: "x",
		AllowedDiffs: []AllowedDiff{
			{PathPattern: "events[*].response.choices[0].message.content", Rule: RuleIgnore, Reason: "ignore content"},
		},
	}
	a := &Snapshot{Backend: "a", Session: &session.Session{Events: []event.Event{*UserEvent("e1", "a")}}}
	b := &Snapshot{Backend: "b", Session: &session.Session{Events: []event.Event{*UserEvent("e1", "b")}}}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) != 0 {
		t.Fatalf("expected allowed: %+v", diffs)
	}
}

func TestComparator_EmptyAllowedRuleNotIgnored(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{
		Name: "x",
		AllowedDiffs: []AllowedDiff{
			{PathPattern: "events[*].response.choices[0].message.content", Rule: "", Reason: "empty should not match"},
		},
	}
	a := &Snapshot{Backend: "a", Session: &session.Session{Events: []event.Event{*UserEvent("e1", "a")}}}
	b := &Snapshot{Backend: "b", Session: &session.Session{Events: []event.Event{*UserEvent("e1", "b")}}}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) == 0 {
		t.Fatalf("empty rule must not allow diff: %+v", diffs)
	}
}

func TestComparator_MemoryAndTrack(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "track_events"}
	ts := time.Unix(10, 0).UTC()
	a := &Snapshot{
		Backend: "a",
		Session: &session.Session{
			Tracks: map[session.Track]*session.TrackEvents{
				"tool": {Track: "tool", Events: []session.TrackEvent{
					{Track: "tool", Payload: []byte(`{"step":1}`), Timestamp: ts},
				}},
			},
		},
		Memories: []*memory.Entry{{ID: "1", Memory: &memory.Memory{Memory: "x", Topics: []string{"t"}, Participants: []string{"p"}}}},
	}
	b := &Snapshot{
		Backend: "b",
		Session: &session.Session{
			Tracks: map[session.Track]*session.TrackEvents{
				"tool": {Track: "tool", Events: []session.TrackEvent{
					{Track: "tool", Payload: []byte(`{"step":2}`), Timestamp: ts.Add(time.Second)},
				}},
			},
		},
		Memories: []*memory.Entry{{ID: "1", Memory: &memory.Memory{Memory: "y", Topics: []string{"t"}, Participants: []string{"q"}}}},
	}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	var hasTrackPayload, hasTrackTS, hasMemContent, hasMemPart bool
	for _, d := range diffs {
		if d.Allowed {
			continue
		}
		switch d.Path {
		case `tracks["tool"].events[0].payload`:
			hasTrackPayload = true
		case `tracks["tool"].events[0].timestamp`:
			hasTrackTS = true
		case "memories[0].content":
			hasMemContent = true
		case "memories[0].participants":
			hasMemPart = true
		}
	}
	if !hasTrackPayload || !hasTrackTS || !hasMemContent || !hasMemPart {
		t.Fatalf("expected track payload/ts + memory content/participants, got %+v", diffs)
	}
}

func TestComparator_BranchLocalSemantic(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "concurrent_interleaved", EventCompareMode: EventCompareBranchLocal}
	ea := []event.Event{*UserEvent("b1.1", "x"), *UserEvent("b2.1", "y")}
	ea[0].Branch, ea[1].Branch = "b1", "b2"
	eb := []event.Event{*UserEvent("b2.1", "y"), *UserEvent("b1.1", "x")}
	eb[0].Branch, eb[1].Branch = "b2", "b1"
	a := &Snapshot{Backend: "a", Session: &session.Session{Events: ea}}
	b := &Snapshot{Backend: "b", Session: &session.Session{Events: eb}}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) != 0 {
		t.Fatalf("branch_local should accept reordered global order: %+v", diffs)
	}

	// content mismatch on same logical id must still fail
	eb2 := []event.Event{*UserEvent("b2.1", "y"), *UserEvent("b1.1", "CHANGED")}
	eb2[0].Branch, eb2[1].Branch = "b2", "b1"
	b2 := &Snapshot{Backend: "b", Session: &session.Session{Events: eb2}}
	b2, _ = n.Normalize(b2)
	diffs = c.Compare(tc, a, b2, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) == 0 {
		t.Fatal("expected semantic content mismatch under branch_local")
	}
}
