//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package seedhistory tracks invocation-scoped session events that originated
// from caller-supplied conversation history.
package seedhistory

import "trpc.group/trpc-go/trpc-agent-go/agent"

// Keep this value aligned with agent.seedHistoryStateKey so Invocation.Clone
// carries the immutable provenance snapshot into descendant invocations.
const stateKey = "__seed_history_event_ids__"

type eventIDs map[string]struct{}

// Mark records an event as caller-supplied seed history for this invocation.
func Mark(inv *agent.Invocation, eventID string) {
	if inv == nil || eventID == "" {
		return
	}
	current, _ := agent.GetStateValue[eventIDs](inv, stateKey)
	// Invocation.Clone shares this internal state value, so publish a new map
	// instead of mutating a snapshot that a descendant may already be reading.
	next := make(eventIDs, len(current)+1)
	for id := range current {
		next[id] = struct{}{}
	}
	next[eventID] = struct{}{}
	inv.SetState(stateKey, next)
}

// Contains reports whether an event originated from caller-supplied seed
// history for this invocation.
func Contains(inv *agent.Invocation, eventID string) bool {
	if inv == nil || eventID == "" {
		return false
	}
	ids, ok := agent.GetStateValue[eventIDs](inv, stateKey)
	if !ok {
		return false
	}
	_, ok = ids[eventID]
	return ok
}
