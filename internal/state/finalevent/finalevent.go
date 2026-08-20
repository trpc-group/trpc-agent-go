//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package finalevent correlates one-shot callbacks with events finalized by
// Runner. It is internal plumbing for components that prepare an event before
// emission but must observe the post-plugin event without a stream-wide hook.
package finalevent

import (
	"context"
	"sync"
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

const stateKeyCallbacks = "__final_event_callbacks__"

type holder struct {
	mu        sync.Mutex
	callbacks map[string][]func(context.Context, *event.Event)
	closed    bool
	pending   atomic.Int64
}

// Attach enables final-event callback registration for an invocation. Runner
// calls Attach before Agent.Run so cloned child invocations share the holder.
func Attach(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	inv.SetState(stateKeyCallbacks, &holder{
		callbacks: make(map[string][]func(context.Context, *event.Event)),
	})
}

// Register associates a one-shot callback with an original event ID. It
// returns false when Runner has not attached a holder or the run has ended.
func Register(
	inv *agent.Invocation,
	eventID string,
	callback func(context.Context, *event.Event),
) bool {
	if inv == nil || eventID == "" || callback == nil {
		return false
	}
	h, ok := agent.GetStateValue[*holder](inv, stateKeyCallbacks)
	if !ok || h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.callbacks[eventID] = append(h.callbacks[eventID], callback)
	h.pending.Add(1)
	return true
}

// Run removes and invokes callbacks for an original event ID. The callback
// receives the event after Runner's OnEvent chain has completed and must treat
// it as read-only.
func Run(
	ctx context.Context,
	inv *agent.Invocation,
	eventID string,
	evt *event.Event,
) {
	callbacks := take(inv, eventID)
	for _, callback := range callbacks {
		callback(ctx, evt)
	}
}

// Discard removes callbacks for an event that Runner will not finalize.
func Discard(inv *agent.Invocation, eventID string) {
	_ = take(inv, eventID)
}

func take(
	inv *agent.Invocation,
	eventID string,
) []func(context.Context, *event.Event) {
	if inv == nil || eventID == "" {
		return nil
	}
	h, ok := agent.GetStateValue[*holder](inv, stateKeyCallbacks)
	if !ok || h == nil {
		return nil
	}
	if h.pending.Load() == 0 {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	callbacks := h.callbacks[eventID]
	delete(h.callbacks, eventID)
	h.pending.Add(-int64(len(callbacks)))
	return callbacks
}

// Clear rejects future registrations and releases pending callbacks for all
// invocation clones that share the holder.
func Clear(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	if h, ok := agent.GetStateValue[*holder](inv, stateKeyCallbacks); ok && h != nil {
		h.mu.Lock()
		h.closed = true
		h.callbacks = nil
		h.pending.Store(0)
		h.mu.Unlock()
	}
	inv.DeleteState(stateKeyCallbacks)
}
