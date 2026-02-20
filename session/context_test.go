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
}

func TestMaskedEventCountAfterTruncation(t *testing.T) {
	sess := NewSession("app", "user", "sess-trunc")
	sess.Events = []event.Event{
		newTestEvent("e1"),
		newTestEvent("e2"),
		newTestEvent("e3"),
	}

	// Mask all three events.
	sess.MaskEvents("e1", "e2", "e3")
	if sess.MaskedEventCount() != 3 {
		t.Fatalf("expected 3, got %d", sess.MaskedEventCount())
	}

	// Simulate event truncation: remove e1 and e2 from the Events slice.
	sess.Events = []event.Event{newTestEvent("e3")}

	// MaskedEventCount should only count the one remaining masked event.
	if sess.MaskedEventCount() != 1 {
		t.Fatalf("after truncation expected 1, got %d", sess.MaskedEventCount())
	}

	// Visible events should be 0 (e3 is still masked).
	visible := sess.GetVisibleEvents()
	if len(visible) != 0 {
		t.Fatalf("expected 0 visible, got %d", len(visible))
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
