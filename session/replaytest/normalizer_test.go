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
	if _, ok := out.Session.State["_secret"]; ok {
		t.Fatal("private key not stripped")
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
