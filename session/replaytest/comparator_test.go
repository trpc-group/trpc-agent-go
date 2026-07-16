// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"testing"

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
				"": {Summary: "full"},
			},
		},
	}
	b := &Snapshot{
		Backend:   "sqlite",
		SessionID: "s1",
		Session: &session.Session{
			Events: []event.Event{*UserEvent("e1", "hello"), *AssistantEvent("e2", "bye")},
			Summaries: map[string]*session.Summary{
				"other": {Summary: "full"},
			},
		},
	}
	// normalize IDs
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

func TestComparator_MemoryAndTrack(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "track_events"}
	a := &Snapshot{
		Backend: "a",
		Session: &session.Session{
			Tracks: map[session.Track]*session.TrackEvents{
				"tool": {Track: "tool", Events: []session.TrackEvent{
					{Track: "tool", Payload: []byte(`{"step":1}`)},
				}},
			},
		},
		Memories: []*memory.Entry{{ID: "1", Memory: &memory.Memory{Memory: "x", Topics: []string{"t"}}}},
	}
	b := &Snapshot{
		Backend: "b",
		Session: &session.Session{
			Tracks: map[session.Track]*session.TrackEvents{
				"tool": {Track: "tool", Events: []session.TrackEvent{
					{Track: "tool", Payload: []byte(`{"step":2}`)},
				}},
			},
		},
		Memories: []*memory.Entry{{ID: "1", Memory: &memory.Memory{Memory: "y", Topics: []string{"t"}}}},
	}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) < 2 {
		t.Fatalf("expected track+memory diffs: %+v", diffs)
	}
}
