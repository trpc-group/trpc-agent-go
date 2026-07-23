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
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestTrapInjector_SwapEventOrder(t *testing.T) {
	trap := TrapSwapEventOrder()
	if trap.Name != "swap_event_order" {
		t.Errorf("expected name 'swap_event_order', got %q", trap.Name)
	}

	result := &BackendResult{
		Session: &session.Session{
			Events: []event.Event{
				{Author: "user"},
				{Author: "assistant"},
			},
		},
	}
	trap.Inject(result)
	if result.Session.Events[0].Author != "assistant" {
		t.Error("expected first event to be 'assistant' after swap")
	}
	if result.Session.Events[1].Author != "user" {
		t.Error("expected second event to be 'user' after swap")
	}
}

func TestTrapInjector_AlterMemoryContent(t *testing.T) {
	trap := TrapAlterMemoryContent()
	if trap.Name != "alter_memory_content" {
		t.Errorf("expected name 'alter_memory_content', got %q", trap.Name)
	}

	result := &BackendResult{
		Memories: []*memory.Entry{
			{Memory: &memory.Memory{Memory: "original content"}},
		},
	}
	trap.Inject(result)
	if result.Memories[0].Memory.Memory != "original content_tampered" {
		t.Errorf("expected tampered content, got %q", result.Memories[0].Memory.Memory)
	}
}

func TestTrapInjector_RemoveSummary(t *testing.T) {
	trap := TrapRemoveSummary()
	if trap.Name != "remove_summary" {
		t.Errorf("expected name 'remove_summary', got %q", trap.Name)
	}

	result := &BackendResult{
		Session: &session.Session{
			Summaries: map[string]*session.Summary{
				"": {Summary: "test summary"},
			},
		},
	}
	if len(result.Session.Summaries) != 1 {
		t.Fatal("expected 1 summary before injection")
	}
	trap.Inject(result)
	if len(result.Session.Summaries) != 0 {
		t.Error("expected 0 summaries after removal")
	}
}

func TestTrapInjector_ShiftTimestamp(t *testing.T) {
	trap := TrapShiftTimestamp()
	now := time.Now().UTC()

	result := &BackendResult{
		Session: &session.Session{
			Events: []event.Event{
				{Timestamp: now},
			},
		},
	}
	trap.Inject(result)
	shifted := result.Session.Events[0].Timestamp
	expected := now.Add(10 * time.Second)
	if !shifted.Equal(expected) {
		t.Errorf("expected timestamp %v, got %v", expected, shifted)
	}
}

func TestTrapInjector_AlterStateValue(t *testing.T) {
	trap := TrapAlterStateValue()

	result := &BackendResult{
		Session: &session.Session{
			State: session.StateMap{"key1": []byte("original")},
		},
	}
	trap.Inject(result)
	if string(result.Session.State["key1"]) != "original_tampered" {
		t.Errorf("expected 'original_tampered', got %q", string(result.Session.State["key1"]))
	}
}

func TestTrapInjector_DuplicateEvent(t *testing.T) {
	trap := TrapDuplicateEvent()

	result := &BackendResult{
		Session: &session.Session{
			Events: []event.Event{
				{Author: "user", ID: "evt-1"},
			},
		},
	}
	if len(result.Session.Events) != 1 {
		t.Fatal("expected 1 event before injection")
	}
	trap.Inject(result)
	if len(result.Session.Events) != 2 {
		t.Errorf("expected 2 events after duplication, got %d", len(result.Session.Events))
	}
}

func TestTrapInjector_AlterFilterKey(t *testing.T) {
	trap := TrapAlterFilterKey()

	result := &BackendResult{
		Session: &session.Session{
			Summaries: map[string]*session.Summary{
				"branch-a": {
					Summary: "test",
					Boundary: &session.SummaryBoundary{
						FilterKey: "branch-a",
					},
				},
			},
		},
	}
	trap.Inject(result)
	for _, s := range result.Session.Summaries {
		if s.Boundary.FilterKey != "_tampered" {
			t.Errorf("expected filter key '_tampered', got %q", s.Boundary.FilterKey)
		}
	}
}

func TestPredefinedTraps_Count(t *testing.T) {
	traps := PredefinedTraps()
	if len(traps) != 7 {
		t.Errorf("expected 7 predefined traps, got %d", len(traps))
	}

	names := make(map[string]bool)
	for _, trap := range traps {
		if names[trap.Name] {
			t.Errorf("duplicate trap name: %s", trap.Name)
		}
		names[trap.Name] = true

		if trap.Inject == nil {
			t.Errorf("trap %q has nil Inject function", trap.Name)
		}
	}
}
