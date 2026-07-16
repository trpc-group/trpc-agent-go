// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func richSnapshot() *Snapshot {
	return &Snapshot{
		Backend:   "baseline",
		SessionID: "s1",
		Session: &session.Session{
			ID: "s1",
			State: session.StateMap{
				"color": []byte("red"),
			},
			Events: []event.Event{
				*UserEvent("e1", "hello"),
				*AssistantEvent("e2", "world"),
			},
			Summaries: map[string]*session.Summary{
				"": {Summary: "full summary"},
			},
			Tracks: map[session.Track]*session.TrackEvents{
				"tool": {
					Track: "tool",
					Events: []session.TrackEvent{
						{Track: "tool", Payload: []byte(`{"step":1}`)},
						{Track: "tool", Payload: []byte(`{"step":2}`)},
					},
				},
			},
		},
		Memories: []*memory.Entry{
			{ID: "m1", Memory: &memory.Memory{Memory: "likes tea", Topics: []string{"prefs"}}},
		},
	}
}

func TestFaultInjection_DetectsAllKinds(t *testing.T) {
	n := NewNormalizer()
	c := NewComparator()
	baseRaw := richSnapshot()
	base, err := n.Normalize(baseRaw)
	if err != nil {
		t.Fatal(err)
	}

	// Map each fault to a representative case name used by comparator special cases.
	caseFor := func(k FaultKind) ReplayCase {
		name := "single_turn_text"
		switch k {
		case FaultDropTrack:
			name = "track_events"
		case FaultMutateMemoryContent, FaultDropMemory:
			name = "memory_write_and_read"
		case FaultDropSummary, FaultOverwriteSummary, FaultWrongSummaryFilterKey:
			name = "summary_generation"
		case FaultMutateState:
			name = "state_crud"
		case FaultReorderEvents:
			name = "multi_turn_conversation"
		}
		return ReplayCase{Name: name}
	}

	detected := 0
	for _, kind := range AllFaultKinds() {
		faultedRaw := richSnapshot()
		if err := InjectFault(faultedRaw, kind); err != nil {
			t.Fatalf("%s inject: %v", kind, err)
		}
		faulted, err := n.Normalize(faultedRaw)
		if err != nil {
			t.Fatalf("%s normalize: %v", kind, err)
		}
		tc := caseFor(kind)
		diffs := c.Compare(tc, base, faulted, InMemoryProfile(), InMemoryProfile())
		if ErrorDiffCount(diffs) == 0 {
			t.Errorf("fault %s not detected; diffs=%+v", kind, diffs)
			continue
		}
		detected++
	}
	if detected != len(AllFaultKinds()) {
		t.Fatalf("detection rate %d/%d", detected, len(AllFaultKinds()))
	}
}

func TestFaultInjection_AgainstLiveInMemoryReplay(t *testing.T) {
	// Build a real baseline snapshot via harness execution, then inject faults.
	b := openInMemoryBackend(t)
	raw, err := executeCase(context.Background(), CaseSummaryGeneration(), b)
	if err != nil {
		t.Fatal(err)
	}
	n := NewNormalizer()
	base, err := n.Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	// re-execute for a clean copy then fault
	raw2, err := executeCase(context.Background(), CaseSummaryGeneration(), openInMemoryBackend(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := InjectFault(raw2, FaultOverwriteSummary); err != nil {
		t.Fatal(err)
	}
	faulted, err := n.Normalize(raw2)
	if err != nil {
		t.Fatal(err)
	}
	diffs := NewComparator().Compare(CaseSummaryGeneration(), base, faulted, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) == 0 {
		t.Fatalf("expected overwrite summary detection, diffs=%+v", diffs)
	}
}

func TestInjectFault_EmptyDropsError(t *testing.T) {
	snap := &Snapshot{Session: &session.Session{}}
	for _, kind := range []FaultKind{FaultDropSummary, FaultDropTrack, FaultDropMemory} {
		if err := InjectFault(snap, kind); err == nil {
			t.Fatalf("%s expected error on empty", kind)
		}
	}
}

func TestInjectFault_WrongSummaryFilterKeyDeterministic(t *testing.T) {
	snap := &Snapshot{Session: &session.Session{
		Summaries: map[string]*session.Summary{
			"z": {Summary: "z"},
			"a": {Summary: "a"},
		},
	}}
	if err := InjectFault(snap, FaultWrongSummaryFilterKey); err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Session.Summaries["wrong-filter-key"]; !ok {
		t.Fatalf("missing rekeyed summary: %+v", snap.Session.Summaries)
	}
	// lexicographically first key "a" should be moved
	if _, ok := snap.Session.Summaries["a"]; ok {
		t.Fatal("key a should have been rekeyed")
	}
	if _, ok := snap.Session.Summaries["z"]; !ok {
		t.Fatal("key z should remain")
	}
}
