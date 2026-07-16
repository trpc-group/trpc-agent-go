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
				e.Timestamp = time.Date(2020, 1, 2, 3, 4, 5, 0, time.FixedZone("CST", 8*3600))
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
	if !out.Session.Events[0].Timestamp.Equal(out.Session.Events[0].Timestamp.UTC()) {
		t.Fatal("timestamp not UTC")
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
	entry := &memory.Entry{
		ID: "rand",
		Memory: &memory.Memory{
			Memory: "likes tea",
			Topics: []string{"b", "a"},
		},
	}
	out, err := n.Normalize(&Snapshot{Memories: []*memory.Entry{entry}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Memories[0].ID == "rand" {
		t.Fatal("id should be content-hash stabilized")
	}
	if out.Memories[0].Memory.Topics[0] != "a" {
		t.Fatalf("topics not sorted: %v", out.Memories[0].Memory.Topics)
	}
}
