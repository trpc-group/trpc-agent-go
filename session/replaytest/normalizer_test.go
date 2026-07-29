// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestNormalizer_EventIDAndPrivateState(t *testing.T) {
	n := NewNormalizer()
	loc := time.FixedZone("CST", 8*3600)
	localTS := time.Date(2020, 1, 2, 3, 4, 5, 0, loc)
	sess := &session.Session{
		ID: "s1",
		State: session.StateMap{
			"color":   []byte("red"),
			"_secret": []byte("x"),
		},
		Events: []event.Event{
			func() event.Event {
				e := UserEvent("logical.1", "hi")
				e.ID = "random-uuid"
				e.Timestamp = localTS
				return *e
			}(),
		},
	}
	snap := &Snapshot{Backend: "inmemory", SessionID: "s1", Session: sess}
	out, err := n.Normalize(snap)
	if err != nil {
		t.Fatal(err)
	}
	if out.Session.Events[0].ID != "logical.1" {
		t.Fatalf("id=%s", out.Session.Events[0].ID)
	}
	got := out.Session.Events[0].Timestamp
	if got.Location() != time.UTC {
		t.Fatalf("timestamp location=%v want UTC", got.Location())
	}
	if !got.Equal(localTS.UTC()) {
		t.Fatalf("timestamp=%v want %v", got, localTS.UTC())
	}
	if _, ok := out.Session.State["_secret"]; !ok {
		t.Fatal("underscore state key should be preserved; use AllowedDiff to ignore")
	}
	if string(out.Session.State["color"]) != "red" {
		t.Fatal("public state lost")
	}
}

// TestNormalizer_SummaryBoundaryLastEventIDRemap ensures summary boundaries track
// rewritten event IDs: different raw backend IDs with the same logical extension
// and matching LastEventID must compare equal after Normalize.
func TestNormalizer_SummaryBoundaryLastEventIDRemap(t *testing.T) {
	n := NewNormalizer()
	cutoff := time.Unix(100, 0).UTC()

	build := func(rawEventID string) *Snapshot {
		ev := UserEvent("logical.boundary", "hello")
		ev.ID = rawEventID
		return &Snapshot{
			Backend:   "b-" + rawEventID,
			SessionID: "s-boundary",
			Session: &session.Session{
				ID:     "s-boundary",
				Events: []event.Event{*ev},
				Summaries: map[string]*session.Summary{
					"": {
						Summary:   "full",
						Topics:    []string{"t"},
						UpdatedAt: cutoff,
						Boundary: &session.SummaryBoundary{
							Version:     session.SummaryBoundaryVersion,
							FilterKey:   "",
							CutoffAt:    cutoff,
							LastEventID: rawEventID,
						},
					},
				},
			},
		}
	}

	a := build("uuid-backend-a")
	b := build("uuid-backend-b")

	// Without remap, raw LastEventID would differ even though events are equivalent.
	if a.Session.Summaries[""].Boundary.LastEventID == b.Session.Summaries[""].Boundary.LastEventID {
		t.Fatal("fixture must use distinct raw event IDs")
	}

	na, err := n.Normalize(a)
	if err != nil {
		t.Fatal(err)
	}
	nb, err := n.Normalize(b)
	if err != nil {
		t.Fatal(err)
	}

	if na.Session.Events[0].ID != "logical.boundary" {
		t.Fatalf("event id a=%q", na.Session.Events[0].ID)
	}
	if nb.Session.Events[0].ID != "logical.boundary" {
		t.Fatalf("event id b=%q", nb.Session.Events[0].ID)
	}
	gotA := na.Session.Summaries[""].Boundary.LastEventID
	gotB := nb.Session.Summaries[""].Boundary.LastEventID
	if gotA != "logical.boundary" || gotB != "logical.boundary" {
		t.Fatalf("boundary LastEventID a=%q b=%q want logical.boundary", gotA, gotB)
	}

	diffs := NewComparator().Compare(
		ReplayCase{Name: "boundary_remap"},
		na, nb,
		InMemoryProfile(), InMemoryProfile(),
	)
	for _, d := range diffs {
		if !d.Allowed && strings.Contains(d.Path, "boundary") {
			t.Fatalf("unexpected boundary diff after remap: %+v", d)
		}
	}
	if ErrorDiffCount(diffs) != 0 {
		t.Fatalf("expected no error diffs, got %+v", diffs)
	}
}

func TestNormalizer_MemoryStableID(t *testing.T) {
	n := NewNormalizer()
	a := &memory.Entry{
		ID: "rand-a",
		Memory: &memory.Memory{
			Memory:       "likes tea",
			Topics:       []string{"b", "a"},
			Participants: []string{"bob", "alice"},
		},
	}
	b := &memory.Entry{
		ID: "rand-b",
		Memory: &memory.Memory{
			Memory:       "likes tea",
			Topics:       []string{"a", "b"},
			Participants: []string{"alice", "bob"},
		},
	}
	c := &memory.Entry{
		ID: "rand-c",
		Memory: &memory.Memory{
			Memory: "likes tea",
			Topics: []string{"other"},
		},
	}
	out, err := n.Normalize(&Snapshot{Memories: []*memory.Entry{a, b, c}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Memories[0].ID == "rand-a" || out.Memories[0].ID == "rand-b" {
		t.Fatal("id should be content-hash stabilized")
	}
	// same semantic payload after topic/participant sort should share ID
	idA, idB, idC := "", "", ""
	for _, m := range out.Memories {
		switch {
		case memoryContent(m) == "likes tea" && len(m.Memory.Topics) == 2:
			if idA == "" {
				idA = m.ID
			} else {
				idB = m.ID
			}
		default:
			idC = m.ID
		}
	}
	if idA == "" || idB == "" {
		// after sort both same-content entries may collapse order; just ensure shared semantic id
		ids := map[string]int{}
		for _, m := range out.Memories {
			ids[m.ID]++
		}
		// two memories with same topics/participants should hash equal
		same := 0
		for _, m := range out.Memories {
			if m.Memory != nil && len(m.Memory.Topics) == 2 {
				same++
				if idA == "" {
					idA = m.ID
				} else if idB == "" {
					idB = m.ID
				}
			} else {
				idC = m.ID
			}
		}
		_ = same
	}
	if idA != idB {
		t.Fatalf("semantic ids differ for equal payload: %s vs %s", idA, idB)
	}
	if idC == "" || idC == idA {
		t.Fatalf("different topics should yield different id, got idC=%s idA=%s", idC, idA)
	}
	if out.Memories[0].Memory.Topics[0] != "a" && out.Memories[0].Memory.Topics[0] != "other" {
		// topics sorted; first memory in sort may be "likes tea"/topics a,b or other
	}
}

func TestNormalizer_KeepsUnderscoreStateKeys(t *testing.T) {
	n := NewNormalizer()
	in := &Snapshot{
		Backend: "a",
		Session: &session.Session{
			State: session.StateMap{
				"_node_metadata":                        []byte("meta"),
				"__trpc_agent_await_user_reply_route__": []byte("route"),
				"color":                                 []byte("red"),
			},
		},
	}
	out, err := n.Normalize(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.Session.State["_node_metadata"]; !ok {
		t.Fatal("expected _node_metadata preserved")
	}
	if _, ok := out.Session.State["__trpc_agent_await_user_reply_route__"]; !ok {
		t.Fatal("expected await route key preserved")
	}
	if string(out.Session.State["color"]) != "red" {
		t.Fatal("color missing")
	}
}

func TestNormalizer_CanonicalizesMemoryAuditTimestamps(t *testing.T) {
	n := NewNormalizer()
	ts1 := time.Unix(100, 0).UTC()
	ts2 := time.Unix(200, 0).UTC()
	eventT := time.Unix(50, 0).In(time.FixedZone("CST", 8*3600))
	in := &Snapshot{
		Backend: "a",
		Memories: []*memory.Entry{{
			ID:        "m1",
			CreatedAt: ts1,
			UpdatedAt: ts2,
			Memory: &memory.Memory{
				Memory: "x", LastUpdated: &ts2, EventTime: &eventT,
			},
		}},
	}
	out, err := n.Normalize(in)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Memories[0].CreatedAt.Equal(FixedTimestamp) || !out.Memories[0].UpdatedAt.Equal(FixedTimestamp) {
		t.Fatalf("audit timestamps not canonicalized: %+v", out.Memories[0])
	}
	if out.Memories[0].Memory.LastUpdated == nil || !out.Memories[0].Memory.LastUpdated.Equal(FixedTimestamp) {
		t.Fatal("LastUpdated not canonicalized")
	}
	if out.Memories[0].Memory.EventTime == nil || out.Memories[0].Memory.EventTime.Location() != time.UTC {
		t.Fatalf("EventTime not UTC: %+v", out.Memories[0].Memory.EventTime)
	}
	if !out.Memories[0].Memory.EventTime.Equal(eventT.UTC()) {
		t.Fatalf("EventTime absolute changed: got %v want %v", out.Memories[0].Memory.EventTime, eventT.UTC())
	}
}

func TestNormalizer_MemorySemanticKeyIncludesEventTimeAndOwnership(t *testing.T) {
	n := NewNormalizer()
	t1 := time.Date(2025, 1, 1, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	t2 := time.Date(2025, 1, 2, 10, 0, 0, 0, time.UTC)
	mk := func(app, user string, eventTime time.Time) *memory.Entry {
		return &memory.Entry{
			ID:      "raw-" + app + "-" + user + "-" + eventTime.Format("20060102"),
			AppName: app,
			UserID:  user,
			Memory:  &memory.Memory{Memory: "same text", EventTime: &eventTime},
		}
	}
	left := &Snapshot{Backend: "a", Memories: []*memory.Entry{
		mk("app", "u1", t1),
		mk("app", "u1", t2),
		mk("app", "u2", t1),
	}}
	right := &Snapshot{Backend: "b", Memories: []*memory.Entry{
		mk("app", "u2", t1),
		mk("app", "u1", t2),
		mk("app", "u1", t1),
	}}
	nl, err := n.Normalize(left)
	if err != nil {
		t.Fatal(err)
	}
	nr, err := n.Normalize(right)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range nl.Memories {
		if seen[m.ID] {
			t.Fatalf("duplicate semantic memory ID after event_time/ownership keying: %s", m.ID)
		}
		seen[m.ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("semantic ids=%v", seen)
	}
	for i := range nl.Memories {
		if nl.Memories[i].ID != nr.Memories[i].ID {
			t.Fatalf("normalized order mismatch at %d: %s vs %s", i, nl.Memories[i].ID, nr.Memories[i].ID)
		}
	}
	diffs := NewComparator().Compare(ReplayCase{Name: "memory_event_time_owner"}, nl, nr, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) != 0 {
		t.Fatalf("expected reversed memory order to align without diffs: %+v", diffs)
	}
}
