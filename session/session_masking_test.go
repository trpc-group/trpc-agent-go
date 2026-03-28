//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// newTestSession creates a session with n events for testing.
func newTestSession(n int) *Session {
	events := make([]event.Event, n)
	for i := 0; i < n; i++ {
		events[i] = event.Event{ID: fmt.Sprintf("evt-%d", i)}
	}
	return &Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		Events:  events,
	}
}

func TestMaskEvents_Basic(t *testing.T) {
	sess := newTestSession(5)

	// Mask two events.
	masked := sess.MaskEvents([]string{"evt-1", "evt-3"})
	assert.Equal(t, 2, masked, "should mask exactly 2 events")

	// Verify they are masked.
	assert.True(t, sess.IsEventMasked("evt-1"))
	assert.True(t, sess.IsEventMasked("evt-3"))
	assert.False(t, sess.IsEventMasked("evt-0"))
	assert.False(t, sess.IsEventMasked("evt-2"))
	assert.False(t, sess.IsEventMasked("evt-4"))
}

func TestMaskEvents_IgnoresNonExistentIDs(t *testing.T) {
	sess := newTestSession(3)

	masked := sess.MaskEvents([]string{"evt-0", "ghost-id", "another-ghost"})
	assert.Equal(t, 1, masked, "should only mask IDs that exist in Events")
	assert.True(t, sess.IsEventMasked("evt-0"))
	assert.False(t, sess.IsEventMasked("ghost-id"))
}

func TestMaskEvents_Idempotent(t *testing.T) {
	sess := newTestSession(3)

	first := sess.MaskEvents([]string{"evt-1"})
	assert.Equal(t, 1, first)

	// Masking the same event again should return 0.
	second := sess.MaskEvents([]string{"evt-1"})
	assert.Equal(t, 0, second, "masking already-masked event returns 0")
	assert.True(t, sess.IsEventMasked("evt-1"))
}

func TestMaskEvents_EmptyList(t *testing.T) {
	sess := newTestSession(3)
	masked := sess.MaskEvents([]string{})
	assert.Equal(t, 0, masked)
	assert.Equal(t, 3, len(sess.GetVisibleEvents()))
}

func TestUnmaskEvents_Basic(t *testing.T) {
	sess := newTestSession(5)

	sess.MaskEvents([]string{"evt-0", "evt-2", "evt-4"})
	assert.Equal(t, 2, len(sess.GetVisibleEvents()))

	unmasked := sess.UnmaskEvents([]string{"evt-2", "evt-4"})
	assert.Equal(t, 2, unmasked)

	assert.True(t, sess.IsEventMasked("evt-0"))
	assert.False(t, sess.IsEventMasked("evt-2"))
	assert.False(t, sess.IsEventMasked("evt-4"))
	assert.Equal(t, 4, len(sess.GetVisibleEvents()))
}

func TestUnmaskEvents_NotMasked(t *testing.T) {
	sess := newTestSession(3)

	// Unmasking events that were never masked returns 0.
	unmasked := sess.UnmaskEvents([]string{"evt-0", "evt-1"})
	assert.Equal(t, 0, unmasked)
}

func TestUnmaskEvents_NilMaskedMap(t *testing.T) {
	sess := newTestSession(3)
	// maskedEventIDs is nil by default.
	unmasked := sess.UnmaskEvents([]string{"evt-0"})
	assert.Equal(t, 0, unmasked)
}

func TestIsEventMasked_NilMaskedMap(t *testing.T) {
	sess := newTestSession(3)
	// Should return false when maskedEventIDs is nil.
	assert.False(t, sess.IsEventMasked("evt-0"))
}

func TestGetVisibleEvents_NoMasking(t *testing.T) {
	sess := newTestSession(5)

	visible := sess.GetVisibleEvents()
	require.Len(t, visible, 5)
	for i, e := range visible {
		assert.Equal(t, fmt.Sprintf("evt-%d", i), e.ID)
	}
}

func TestGetVisibleEvents_WithMasking(t *testing.T) {
	sess := newTestSession(5)

	sess.MaskEvents([]string{"evt-1", "evt-3"})

	visible := sess.GetVisibleEvents()
	require.Len(t, visible, 3)
	assert.Equal(t, "evt-0", visible[0].ID)
	assert.Equal(t, "evt-2", visible[1].ID)
	assert.Equal(t, "evt-4", visible[2].ID)
}

func TestGetVisibleEvents_AllMasked(t *testing.T) {
	sess := newTestSession(3)

	sess.MaskEvents([]string{"evt-0", "evt-1", "evt-2"})
	visible := sess.GetVisibleEvents()
	assert.Empty(t, visible)
}

func TestGetVisibleEvents_PreservesOrder(t *testing.T) {
	sess := newTestSession(10)

	// Mask even-indexed events.
	toMask := []string{"evt-0", "evt-2", "evt-4", "evt-6", "evt-8"}
	sess.MaskEvents(toMask)

	visible := sess.GetVisibleEvents()
	require.Len(t, visible, 5)
	for i, e := range visible {
		expected := fmt.Sprintf("evt-%d", 2*i+1)
		assert.Equal(t, expected, e.ID, "order should be preserved")
	}
}

func TestGetVisibleEvents_ReturnsCopy(t *testing.T) {
	sess := newTestSession(3)

	visible := sess.GetVisibleEvents()
	// Mutating the returned slice should not affect the session.
	visible[0].ID = "mutated"
	assert.Equal(t, "evt-0", sess.Events[0].ID, "should return a copy")
}

func TestMaskUnmask_RoundTrip(t *testing.T) {
	sess := newTestSession(5)

	// Mask all, verify empty, unmask all, verify full.
	allIDs := []string{"evt-0", "evt-1", "evt-2", "evt-3", "evt-4"}
	sess.MaskEvents(allIDs)
	assert.Empty(t, sess.GetVisibleEvents())

	sess.UnmaskEvents(allIDs)
	assert.Len(t, sess.GetVisibleEvents(), 5)

	// All should be unmasked.
	for _, id := range allIDs {
		assert.False(t, sess.IsEventMasked(id))
	}
}

func TestGetEvents_IncludesMaskedEvents(t *testing.T) {
	sess := newTestSession(5)

	sess.MaskEvents([]string{"evt-1", "evt-3"})

	// GetEvents should return ALL events (including masked ones).
	all := sess.GetEvents()
	assert.Len(t, all, 5, "GetEvents returns all events regardless of masking")

	// GetVisibleEvents should exclude masked events.
	visible := sess.GetVisibleEvents()
	assert.Len(t, visible, 3, "GetVisibleEvents excludes masked events")
}

func TestMaskEvents_ConcurrentSafety(t *testing.T) {
	sess := newTestSession(100)

	var wg sync.WaitGroup
	// Concurrently mask disjoint sets of events.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(batch int) {
			defer wg.Done()
			ids := make([]string, 10)
			for j := 0; j < 10; j++ {
				ids[j] = fmt.Sprintf("evt-%d", batch*10+j)
			}
			sess.MaskEvents(ids)
		}(i)
	}
	wg.Wait()

	// All 100 events should be masked.
	for i := 0; i < 100; i++ {
		assert.True(t, sess.IsEventMasked(fmt.Sprintf("evt-%d", i)),
			"event %d should be masked", i)
	}
	assert.Empty(t, sess.GetVisibleEvents())
}

func TestGetVisibleEvents_ConcurrentReadSafety(t *testing.T) {
	sess := newTestSession(50)
	sess.MaskEvents([]string{"evt-0", "evt-25", "evt-49"})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			visible := sess.GetVisibleEvents()
			assert.Len(t, visible, 47)
		}()
	}
	wg.Wait()
}

func TestMaskEvents_ConcurrentMaskAndRead(t *testing.T) {
	sess := newTestSession(50)

	var wg sync.WaitGroup

	// Writer: mask events.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			sess.MaskEvents([]string{fmt.Sprintf("evt-%d", i)})
		}
	}()

	// Reader: read visible events concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			visible := sess.GetVisibleEvents()
			// Length should monotonically decrease from 50 to 25.
			assert.GreaterOrEqual(t, len(visible), 25)
			assert.LessOrEqual(t, len(visible), 50)
		}
	}()

	wg.Wait()
}
