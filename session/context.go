//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package session

import (
	"trpc.group/trpc-go/trpc-agent-go/event"
)

// MaskEvents soft-hides events by their IDs from the LLM's visible context.
// Masked events remain in the Events slice for audit and debugging but are
// excluded from GetVisibleEvents(). This implements the Pensieve paradigm's
// "deleteContext" operation: the model can prune processed information from
// its context window while retaining summaries and notes.
//
// Thread-safe: protected by EventMu.
func (sess *Session) MaskEvents(ids ...string) int {
	if sess == nil || len(ids) == 0 {
		return 0
	}

	sess.EventMu.Lock()
	defer sess.EventMu.Unlock()

	if sess.maskedEventIDs == nil {
		sess.maskedEventIDs = make(map[string]bool, len(ids))
	}

	// Build a set of existing event IDs so we only mask IDs that are present.
	existingIDs := make(map[string]struct{}, len(sess.Events))
	for _, e := range sess.Events {
		existingIDs[e.ID] = struct{}{}
	}

	masked := 0
	for _, id := range ids {
		if _, exists := existingIDs[id]; exists && !sess.maskedEventIDs[id] {
			sess.maskedEventIDs[id] = true
			masked++
		}
	}
	return masked
}

// UnmaskEvents restores previously masked events to the LLM's visible context.
// Returns the number of events that were actually unmasked.
//
// Thread-safe: protected by EventMu.
func (sess *Session) UnmaskEvents(ids ...string) int {
	if sess == nil || len(ids) == 0 {
		return 0
	}

	sess.EventMu.Lock()
	defer sess.EventMu.Unlock()

	if len(sess.maskedEventIDs) == 0 {
		return 0
	}

	unmasked := 0
	for _, id := range ids {
		if sess.maskedEventIDs[id] {
			delete(sess.maskedEventIDs, id)
			unmasked++
		}
	}
	return unmasked
}

// GetVisibleEvents returns only the events that have not been masked.
// This is the Pensieve paradigm's view of the interaction history —
// the model sees only the events it hasn't pruned. Use this instead of
// accessing Events directly when building LLM prompts.
//
// Thread-safe: protected by EventMu.
func (sess *Session) GetVisibleEvents() event.Events {
	if sess == nil {
		return nil
	}

	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	if len(sess.maskedEventIDs) == 0 {
		// Fast path: no masking, return a copy of all events.
		out := make(event.Events, len(sess.Events))
		copy(out, sess.Events)
		return out
	}

	out := make(event.Events, 0, len(sess.Events))
	for _, e := range sess.Events {
		if !sess.maskedEventIDs[e.ID] {
			out = append(out, e)
		}
	}
	return out
}

// MaskedEventCount returns the number of currently masked events that still
// exist in the Events slice. Stale mask entries for truncated events are not
// counted, which prevents check_budget's visible_events from going negative.
//
// Thread-safe: protected by EventMu.
func (sess *Session) MaskedEventCount() int {
	if sess == nil {
		return 0
	}

	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	if len(sess.maskedEventIDs) == 0 {
		return 0
	}

	count := 0
	for _, e := range sess.Events {
		if sess.maskedEventIDs[e.ID] {
			count++
		}
	}
	return count
}

// IsEventMasked returns whether a given event ID is currently masked.
//
// IMPORTANT: This method does NOT acquire EventMu. The caller MUST hold
// EventMu (at least RLock) before calling this method. This design allows
// ContentRequestProcessor (which already locks EventMu when iterating
// sess.Events) to check mask status without a nested lock.
func (sess *Session) IsEventMasked(id string) bool {
	if sess == nil || len(sess.maskedEventIDs) == 0 {
		return false
	}

	return sess.maskedEventIDs[id]
}
