//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package session

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func newTestEvent(id string) event.Event {
	return event.Event{
		Response: &model.Response{},
		ID:       id,
		Author:   "test",
	}
}

func TestMaskEvents(t *testing.T) {
	t.Run("masks events and returns count", func(t *testing.T) {
		sess := NewSession("app", "user", "sess-1")
		sess.Events = []event.Event{
			newTestEvent("e1"),
			newTestEvent("e2"),
			newTestEvent("e3"),
		}

		n := sess.MaskEvents("e1", "e3")
		if n != 2 {
			t.Fatalf("expected 2 masked, got %d", n)
		}

		visible := sess.GetVisibleEvents()
		if len(visible) != 1 {
			t.Fatalf("expected 1 visible, got %d", len(visible))
		}
		if visible[0].ID != "e2" {
			t.Fatalf("expected e2 visible, got %s", visible[0].ID)
		}
	})

	t.Run("idempotent masking", func(t *testing.T) {
		sess := NewSession("app", "user", "sess-2")
		sess.Events = []event.Event{newTestEvent("e1")}

		n1 := sess.MaskEvents("e1")
		n2 := sess.MaskEvents("e1")
		if n1 != 1 || n2 != 0 {
			t.Fatalf("expected (1,0), got (%d,%d)", n1, n2)
		}
	})

	t.Run("nil session", func(t *testing.T) {
		var sess *Session
		n := sess.MaskEvents("e1")
		if n != 0 {
			t.Fatalf("expected 0, got %d", n)
		}
	})

	t.Run("empty ids", func(t *testing.T) {
		sess := NewSession("app", "user", "sess-3")
		n := sess.MaskEvents()
		if n != 0 {
			t.Fatalf("expected 0, got %d", n)
		}
	})

	t.Run("ignores non-existent event IDs", func(t *testing.T) {
		sess := NewSession("app", "user", "sess-nonexist")
		sess.Events = []event.Event{
			newTestEvent("e1"),
			newTestEvent("e2"),
		}

		n := sess.MaskEvents("e1", "e3")
		if n != 1 {
			t.Fatalf("expected 1 masked (only e1), got %d", n)
		}

		visible := sess.GetVisibleEvents()
		if len(visible) != 1 || visible[0].ID != "e2" {
			t.Fatalf("expected only e2 visible, got %v", visible)
		}
	})
}

func TestUnmaskEvents(t *testing.T) {
	t.Run("unmasks previously masked events", func(t *testing.T) {
		sess := NewSession("app", "user", "sess-4")
		sess.Events = []event.Event{
			newTestEvent("e1"),
			newTestEvent("e2"),
		}

		sess.MaskEvents("e1", "e2")
		if len(sess.GetVisibleEvents()) != 0 {
			t.Fatal("expected 0 visible after masking both")
		}

		n := sess.UnmaskEvents("e1")
		if n != 1 {
			t.Fatalf("expected 1 unmasked, got %d", n)
		}

		visible := sess.GetVisibleEvents()
		if len(visible) != 1 || visible[0].ID != "e1" {
			t.Fatal("expected e1 visible after unmasking")
		}
	})

	t.Run("unmasking non-masked is no-op", func(t *testing.T) {
		sess := NewSession("app", "user", "sess-5")
		n := sess.UnmaskEvents("nonexistent")
		if n != 0 {
			t.Fatalf("expected 0, got %d", n)
		}
	})

	t.Run("nil session and empty ids are no-op", func(t *testing.T) {
		var sess *Session
		if n := sess.UnmaskEvents("e1"); n != 0 {
			t.Fatalf("expected 0 for nil session, got %d", n)
		}
		sess = NewSession("app", "user", "sess-unmask-empty")
		if n := sess.UnmaskEvents(); n != 0 {
			t.Fatalf("expected 0 for empty ids, got %d", n)
		}
	})
}

func TestGetVisibleEvents(t *testing.T) {
	t.Run("returns all events when none masked", func(t *testing.T) {
		sess := NewSession("app", "user", "sess-6")
		sess.Events = []event.Event{
			newTestEvent("e1"),
			newTestEvent("e2"),
		}

		visible := sess.GetVisibleEvents()
		if len(visible) != 2 {
			t.Fatalf("expected 2 visible, got %d", len(visible))
		}
	})

	t.Run("returns empty slice when all masked", func(t *testing.T) {
		sess := NewSession("app", "user", "sess-7")
		sess.Events = []event.Event{newTestEvent("e1")}
		sess.MaskEvents("e1")

		visible := sess.GetVisibleEvents()
		if len(visible) != 0 {
			t.Fatalf("expected 0 visible, got %d", len(visible))
		}
	})

	t.Run("nil session", func(t *testing.T) {
		var sess *Session
		visible := sess.GetVisibleEvents()
		if visible != nil {
			t.Fatal("expected nil for nil session")
		}
	})
}

func TestMaskedEventCount(t *testing.T) {
	sess := NewSession("app", "user", "sess-8")
	sess.Events = []event.Event{
		newTestEvent("e1"),
		newTestEvent("e2"),
	}
	if sess.MaskedEventCount() != 0 {
		t.Fatal("expected 0 initially")
	}

	sess.MaskEvents("e1", "e2")
	if sess.MaskedEventCount() != 2 {
		t.Fatalf("expected 2, got %d", sess.MaskedEventCount())
	}

	// Ghost IDs must not inflate the masked count.
	if n := sess.MaskEvents("missing-id"); n != 0 {
		t.Fatalf("expected 0 for non-existent id, got %d", n)
	}
	if sess.MaskedEventCount() != 2 {
		t.Fatalf("expected masked count to stay 2, got %d", sess.MaskedEventCount())
	}

	var nilSess *Session
	if nilSess.MaskedEventCount() != 0 {
		t.Fatal("expected 0 for nil session")
	}
}

func TestClonePreservesMaskedEvents(t *testing.T) {
	sess := NewSession("app", "user", "sess-9")
	sess.Events = []event.Event{
		newTestEvent("e1"),
		newTestEvent("e2"),
		newTestEvent("e3"),
	}
	sess.MaskEvents("e2")

	cloned := sess.Clone()

	// Clone should have the same mask.
	if cloned.MaskedEventCount() != 1 {
		t.Fatal("clone should preserve masked count")
	}
	visible := cloned.GetVisibleEvents()
	if len(visible) != 2 {
		t.Fatalf("expected 2 visible in clone, got %d", len(visible))
	}

	// Mutating clone's mask should not affect original.
	cloned.MaskEvents("e3")
	if sess.MaskedEventCount() != 1 {
		t.Fatal("original mask should be unaffected by clone mutation")
	}
}

func TestIsEventMasked(t *testing.T) {
	sess := NewSession("app", "user", "sess-masked")
	sess.Events = []event.Event{
		newTestEvent("e1"),
		newTestEvent("e2"),
		newTestEvent("e3"),
	}
	sess.MaskEvents("e1", "e3")

	tests := []struct {
		id   string
		want bool
	}{
		{id: "e1", want: true},
		{id: "e3", want: true},
		{id: "e2", want: false},
		{id: "e999", want: false},
		{id: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			if got := sess.IsEventMasked(tt.id); got != tt.want {
				t.Fatalf("IsEventMasked(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}

	var nilSess *Session
	if nilSess.IsEventMasked("e1") {
		t.Fatal("expected false for nil session")
	}

	t.Run("returns false when no events are masked", func(t *testing.T) {
		fresh := NewSession("app", "user", "sess-unmasked")
		fresh.Events = []event.Event{newTestEvent("e1")}
		if fresh.IsEventMasked("e1") {
			t.Fatal("expected false before any masks are applied")
		}
	})
}

func TestHydrateMaskedEventsFromState(t *testing.T) {
	sess := NewSession("app", "user", "sess-hydrate")
	sess.Events = []event.Event{
		newTestEvent("e1"),
		newTestEvent("e2"),
	}
	payload, err := marshalMaskedEventIDs([]string{"e1"})
	if err != nil {
		t.Fatal(err)
	}
	sess.SetState(MaskedEventsStateKey, payload)

	fresh := NewSession("app", "user", "sess-hydrate-fresh")
	fresh.Events = sess.Events
	fresh.SetState(MaskedEventsStateKey, payload)

	visible := fresh.GetVisibleEvents()
	if len(visible) != 1 || visible[0].ID != "e2" {
		t.Fatalf("expected only e2 visible after hydration, got %v", visible)
	}
}

func TestHydrateIgnoresCorruptMaskedState(t *testing.T) {
	sess := NewSession("app", "user", "sess-corrupt")
	sess.Events = []event.Event{newTestEvent("e1"), newTestEvent("e2")}
	sess.SetState(MaskedEventsStateKey, []byte("not-json"))

	visible := sess.GetVisibleEvents()
	if len(visible) != 2 {
		t.Fatalf("expected corrupt state to be ignored, got %d visible", len(visible))
	}
}

func TestSyncMaskedEventsToState(t *testing.T) {
	t.Run("writes empty array when no masks", func(t *testing.T) {
		sess := NewSession("app", "user", "sync-empty")
		payload, err := sess.SyncMaskedEventsToState()
		if err != nil {
			t.Fatal(err)
		}
		if string(payload) != "[]" {
			t.Fatalf("expected [], got %s", payload)
		}
		stored, ok := sess.GetState(MaskedEventsStateKey)
		if !ok || string(stored) != "[]" {
			t.Fatalf("expected empty array in state, got %q ok=%v", stored, ok)
		}
	})

	t.Run("nil session", func(t *testing.T) {
		var sess *Session
		payload, err := sess.SyncMaskedEventsToState()
		if err != nil || payload != nil {
			t.Fatalf("expected nil,nil got payload=%v err=%v", payload, err)
		}
	})

	t.Run("writes masked ids to state", func(t *testing.T) {
		sess := NewSession("app", "user", "sync-masked")
		sess.Events = []event.Event{newTestEvent("e1"), newTestEvent("e2")}
		sess.MaskEvents("e1")

		payload, err := sess.SyncMaskedEventsToState()
		if err != nil {
			t.Fatal(err)
		}
		if string(payload) != `["e1"]` {
			t.Fatalf("unexpected payload: %s", payload)
		}
	})
}

func TestHydrateEmptyMaskedStateList(t *testing.T) {
	sess := NewSession("app", "user", "sess-empty-mask-list")
	sess.Events = []event.Event{newTestEvent("e1"), newTestEvent("e2")}
	sess.SetState(MaskedEventsStateKey, []byte("[]"))

	if len(sess.GetVisibleEvents()) != 2 {
		t.Fatal("expected empty persisted mask list to leave all events visible")
	}
}

func newToolCallEvent(id string, toolCallIDs ...string) event.Event {
	calls := make([]model.ToolCall, len(toolCallIDs))
	for i, callID := range toolCallIDs {
		calls[i] = model.ToolCall{
			ID: callID,
			Function: model.FunctionDefinitionParam{
				Name: "lookup",
			},
		}
	}
	return event.Event{
		ID:     id,
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:      model.RoleAssistant,
					ToolCalls: calls,
				},
			}},
		},
	}
}

func newToolResultEvent(id, toolCallID, content string) event.Event {
	return event.Event{
		ID:     id,
		Author: "tool",
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleTool,
					ToolID:  toolCallID,
					Content: content,
				},
			}},
		},
	}
}

func TestMaskEvents_expandsToolCallRound(t *testing.T) {
	sess := NewSession("app", "user", "tool-round")
	sess.Events = []event.Event{
		newTestEvent("user-1"),
		newToolCallEvent("call-1", "tc-1"),
		newToolResultEvent("result-1", "tc-1", "answer"),
	}

	masked := sess.MaskEvents("result-1")
	if masked != 2 {
		t.Fatalf("expected call+result masked, got %d", masked)
	}
	visible := sess.GetVisibleEvents()
	if len(visible) != 1 || visible[0].ID != "user-1" {
		t.Fatalf("expected only user event visible, got %v", visible)
	}
}

func TestHydrateSkipsEmptyMaskedIDs(t *testing.T) {
	sess := NewSession("app", "user", "sess-skip-empty-id")
	sess.Events = []event.Event{newTestEvent("e1"), newTestEvent("e2")}
	sess.SetState(MaskedEventsStateKey, []byte(`["","e1"]`))

	visible := sess.GetVisibleEvents()
	if len(visible) != 1 || visible[0].ID != "e2" {
		t.Fatalf("expected only e2 visible, got %v", visible)
	}
}
