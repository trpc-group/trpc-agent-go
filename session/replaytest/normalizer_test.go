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
