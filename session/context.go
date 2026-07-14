//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

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

	sess.ensureMaskedEventsFromStateLocked()

	if sess.maskedEventIDs == nil {
		sess.maskedEventIDs = make(map[string]bool, len(ids))
	}

	// Build a set of existing event IDs so we only mask IDs that are present.
	existingIDs := make(map[string]struct{}, len(sess.Events))
	for _, e := range sess.Events {
		existingIDs[e.ID] = struct{}{}
	}

	idsToMask := expandMaskIDsForToolRounds(sess.Events, ids)

	masked := 0
	for _, id := range idsToMask {
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

	sess.ensureMaskedEventsFromStateLocked()

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
func (sess *Session) GetVisibleEvents() []event.Event {
	if sess == nil {
		return nil
	}

	sess.ensureMaskedEventsFromState()

	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	if len(sess.maskedEventIDs) == 0 {
		// Fast path: no masking, return a copy of all events.
		out := make([]event.Event, len(sess.Events))
		copy(out, sess.Events)
		return out
	}

	out := make([]event.Event, 0, len(sess.Events))
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

	sess.ensureMaskedEventsFromState()

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
// Thread-safe: protected by EventMu.
func (sess *Session) IsEventMasked(id string) bool {
	if sess == nil || id == "" {
		return false
	}

	sess.ensureMaskedEventsFromState()

	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	if len(sess.maskedEventIDs) == 0 {
		return false
	}

	return sess.maskedEventIDs[id]
}

// SyncMaskedEventsToState serializes the current masked-event set into session
// state so it survives session reloads from a session service.
func (sess *Session) SyncMaskedEventsToState() ([]byte, error) {
	payload, err := sess.maskedEventsPayload()
	if err != nil || sess == nil {
		return payload, err
	}
	sess.SetState(MaskedEventsStateKey, payload)
	return payload, nil
}

// PersistMaskedEvents writes the masked-event set to session state and, when
// svc is non-nil, persists it through the session service.
func (sess *Session) PersistMaskedEvents(
	ctx context.Context,
	svc Service,
	key Key,
) error {
	payload, err := sess.maskedEventsPayload()
	if err != nil {
		return err
	}
	if svc == nil {
		if sess != nil {
			sess.SetState(MaskedEventsStateKey, payload)
		}
		return nil
	}
	if err := svc.UpdateSessionState(ctx, key, StateMap{
		MaskedEventsStateKey: payload,
	}); err != nil {
		return fmt.Errorf("update session state for masked events: %w", err)
	}
	sess.SetState(MaskedEventsStateKey, payload)
	return nil
}

func (sess *Session) maskedEventsPayload() ([]byte, error) {
	if sess == nil {
		return nil, nil
	}

	sess.ensureMaskedEventsFromState()

	sess.EventMu.RLock()
	ids := sess.maskedEventIDListLocked()
	sess.EventMu.RUnlock()

	return marshalMaskedEventIDs(ids)
}

func (sess *Session) ensureMaskedEventsFromState() {
	if sess == nil {
		return
	}

	sess.EventMu.Lock()
	defer sess.EventMu.Unlock()
	sess.ensureMaskedEventsFromStateLocked()
}

func (sess *Session) ensureMaskedEventsFromStateLocked() {
	if sess.maskedEventsHydrated {
		return
	}
	sess.maskedEventsHydrated = true

	raw, ok := sess.GetState(MaskedEventsStateKey)
	if !ok || len(raw) == 0 {
		return
	}

	ids, err := unmarshalMaskedEventIDs(raw)
	if err != nil || len(ids) == 0 {
		return
	}

	if sess.maskedEventIDs == nil {
		sess.maskedEventIDs = make(map[string]bool, len(ids))
	}
	for _, id := range ids {
		if id != "" {
			sess.maskedEventIDs[id] = true
		}
	}
}

func (sess *Session) maskedEventIDListLocked() []string {
	if len(sess.maskedEventIDs) == 0 {
		return nil
	}
	present := make(map[string]struct{}, len(sess.Events))
	for _, e := range sess.Events {
		if e.ID != "" {
			present[e.ID] = struct{}{}
		}
	}
	ids := make([]string, 0, len(sess.maskedEventIDs))
	for id := range sess.maskedEventIDs {
		if _, ok := present[id]; !ok {
			delete(sess.maskedEventIDs, id)
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func marshalMaskedEventIDs(ids []string) ([]byte, error) {
	if len(ids) == 0 {
		return []byte("[]"), nil
	}
	payload, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal masked event ids: %w", err)
	}
	return payload, nil
}

func unmarshalMaskedEventIDs(raw []byte) ([]string, error) {
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, fmt.Errorf("unmarshal masked event ids: %w", err)
	}
	return ids, nil
}
