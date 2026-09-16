//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package messageorigin tracks invocation-scoped origins for session messages
// persisted by the runner before agent execution begins.
package messageorigin

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/messageoriginkey"
)

type origin uint8

const (
	originSeedHistory origin = 1 << iota
	originCurrentTurn
)

type eventOrigins map[string]origin

// MarkSeedHistory records an event as caller-supplied seed history.
func MarkSeedHistory(inv *agent.Invocation, eventID string) {
	mark(inv, eventID, originSeedHistory)
}

// MarkCurrentTurn records an event as input for the active turn.
func MarkCurrentTurn(inv *agent.Invocation, eventID string) {
	mark(inv, eventID, originCurrentTurn)
}

func mark(inv *agent.Invocation, eventID string, value origin) {
	if inv == nil || eventID == "" {
		return
	}
	current, _ := agent.GetStateValue[eventOrigins](inv, messageoriginkey.Key)
	// Invocation.Clone shares this internal state value, so publish a new map
	// instead of mutating a snapshot that a descendant may already be reading.
	next := make(eventOrigins, len(current)+1)
	for id, currentOrigin := range current {
		next[id] = currentOrigin
	}
	next[eventID] |= value
	inv.SetState(messageoriginkey.Key, next)
}

// IsSeedHistory reports whether an event originated from caller-supplied seed
// history.
func IsSeedHistory(inv *agent.Invocation, eventID string) bool {
	return contains(inv, eventID, originSeedHistory)
}

// IsCurrentTurn reports whether an event is input for the active turn.
func IsCurrentTurn(inv *agent.Invocation, eventID string) bool {
	return contains(inv, eventID, originCurrentTurn)
}

func contains(inv *agent.Invocation, eventID string, value origin) bool {
	if inv == nil || eventID == "" {
		return false
	}
	origins, ok := agent.GetStateValue[eventOrigins](inv, messageoriginkey.Key)
	if !ok {
		return false
	}
	return origins[eventID]&value != 0
}
