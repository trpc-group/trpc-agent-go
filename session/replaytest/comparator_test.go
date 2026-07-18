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
	"trpc.group/trpc-go/trpc-agent-go/model"
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

func TestComparator_BranchLocalDuplicateIDOccurrence(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "dup", EventCompareMode: EventCompareBranchLocal}
	// Same multiset of IDs and branch order, but first occurrence content differs.
	ea := []event.Event{*UserEvent("same", "first-a"), *UserEvent("same", "second")}
	ea[0].Branch, ea[1].Branch = "b1", "b1"
	eb := []event.Event{*UserEvent("same", "first-B"), *UserEvent("same", "second")}
	eb[0].Branch, eb[1].Branch = "b1", "b1"
	a := &Snapshot{Backend: "a", Session: &session.Session{Events: ea}}
	b := &Snapshot{Backend: "b", Session: &session.Session{Events: eb}}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) == 0 {
		t.Fatalf("expected occurrence-aware semantic mismatch, got none: %+v", diffs)
	}
	var hasOcc bool
	for _, d := range diffs {
		if !d.Allowed && (contains(d.Path, "id=same#0") || contains(d.Path, "content")) {
			hasOcc = true
		}
	}
	if !hasOcc {
		t.Fatalf("expected first-occurrence content path, got %+v", diffs)
	}
}

func TestComparator_EventInvocationAndMemoryFields(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "fields"}
	ea := *UserEvent("e1", "hi")
	ea.Tag = "t1"
	ea.RequiresCompletion = true
	ea.FilterKey = "fk"
	ea.Version = 2
	ea.InvocationID = "inv-a"
	eb := *UserEvent("e1", "hi")
	eb.Tag = "t2"
	eb.RequiresCompletion = false
	eb.FilterKey = "fk2"
	eb.Version = 3
	eb.InvocationID = "inv-b"
	ts := time.Unix(100, 0).UTC()
	ts2 := time.Unix(200, 0).UTC()
	a := &Snapshot{
		Backend: "a",
		Session: &session.Session{Events: []event.Event{ea}},
		Memories: []*memory.Entry{{
			ID: "m",
			Memory: &memory.Memory{
				Memory: "x", Kind: memory.KindFact, Location: "home",
				EventTime: &ts, LastUpdated: &ts,
			},
		}},
	}
	b := &Snapshot{
		Backend: "b",
		Session: &session.Session{Events: []event.Event{eb}},
		Memories: []*memory.Entry{{
			ID: "m",
			Memory: &memory.Memory{
				Memory: "x", Kind: memory.KindEpisode, Location: "office",
				EventTime: &ts2, LastUpdated: &ts2,
			},
		}},
	}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	wantPaths := []string{
		"events[0].tag",
		"events[0].requires_completion",
		"events[0].filter_key",
		"events[0].version",
		"events[0].invocation_id",
		"memories[0].kind",
		"memories[0].location",
		"memories[0].event_time",
	}
	got := map[string]bool{}
	for _, d := range diffs {
		if !d.Allowed {
			got[d.Path] = true
		}
	}
	for _, pth := range wantPaths {
		if !got[pth] {
			t.Fatalf("missing path %s in %+v", pth, diffs)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (len(s) > 0 && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()))
}

func TestComparator_ResponseResidual(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "resp"}
	ea := *UserEvent("e1", "hi")
	eb := *UserEvent("e1", "hi")
	if ea.Response == nil || eb.Response == nil {
		t.Fatal("fixture missing response")
	}
	ea.Response.Object = "chat.completion"
	ea.Response.Done = true
	eb.Response.Object = "chat.completion.chunk"
	eb.Response.Done = false
	eb.Response.Error = &model.ResponseError{Message: "boom"}
	a := &Snapshot{Backend: "a", Session: &session.Session{Events: []event.Event{ea}}}
	b := &Snapshot{Backend: "b", Session: &session.Session{Events: []event.Event{eb}}}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	var has bool
	for _, d := range diffs {
		if !d.Allowed && d.Path == "events[0].response" {
			has = true
		}
	}
	if !has {
		t.Fatalf("expected response residual diff, got %+v", diffs)
	}
}

func TestComparator_BranchLocalCrossBranchDuplicateID(t *testing.T) {
	cmp := NewComparator()
	tc := ReplayCase{Name: "cross_branch_dup", EventCompareMode: EventCompareBranchLocal}
	// Same ID appears once on each branch. Global interleaving differs; branch-local
	// occurrence counters must not cross-pair events across branches.
	a := &Snapshot{Session: &session.Session{Events: []event.Event{
		{ID: "same", Author: "user", Branch: "b1", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "b1"}}}}},
		{ID: "same", Author: "user", Branch: "b2", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "b2"}}}}},
	}}}
	b := &Snapshot{Session: &session.Session{Events: []event.Event{
		{ID: "same", Author: "user", Branch: "b2", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "b2"}}}}},
		{ID: "same", Author: "user", Branch: "b1", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "b1"}}}}},
	}}}
	diffs := cmp.Compare(tc, a, b, BackendProfile{Name: "A"}, BackendProfile{Name: "B"})
	if len(diffs) != 0 {
		t.Fatalf("branch-local cross-branch same ID with reordered interleaving should pass: %+v", diffs)
	}

	// Content mismatch on b1 must still be detected and not swallowed by global pairing.
	bBad := &Snapshot{Session: &session.Session{Events: []event.Event{
		{ID: "same", Author: "user", Branch: "b2", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "b2"}}}}},
		{ID: "same", Author: "user", Branch: "b1", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "B1-BAD"}}}}},
	}}}
	diffs = cmp.Compare(tc, a, bBad, BackendProfile{Name: "A"}, BackendProfile{Name: "B"})
	if len(diffs) == 0 {
		t.Fatal("expected content mismatch for branch b1 event")
	}
}

func TestComparator_SessionPresence(t *testing.T) {
	cmp := NewComparator()
	tc := ReplayCase{Name: "session_presence"}
	a := &Snapshot{Session: &session.Session{Events: nil}}
	b := &Snapshot{Session: nil}
	diffs := cmp.Compare(tc, a, b, BackendProfile{Name: "A"}, BackendProfile{Name: "B"})
	found := false
	for _, d := range diffs {
		if d.Path == "session" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected session presence mismatch, got %+v", diffs)
	}
}

func TestComparator_MemoryPresence(t *testing.T) {
	cmp := NewComparator()
	tc := ReplayCase{Name: "memory_presence"}
	a := &Snapshot{Memories: []*memory.Entry{nil}}
	b := &Snapshot{Memories: []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "x"}}}}
	diffs := cmp.Compare(tc, a, b, BackendProfile{Name: "A"}, BackendProfile{Name: "B"})
	found := false
	for _, d := range diffs {
		if d.Path == "memories[0]" || d.Path == "memories[0].memory" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected memory presence mismatch, got %+v", diffs)
	}
}
