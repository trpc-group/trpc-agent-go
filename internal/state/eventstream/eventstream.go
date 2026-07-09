//
// Tencent is pleased to support the open source community by making trpc-agent-go
// available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package eventstream lets components that execute nested work outside the
// root agent event channel forward events through the active Runner loop.
package eventstream

import (
	"context"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

// StateKeyForwardEvent is the invocation state key used by Attach.
const StateKeyForwardEvent = "__forward_event__"

// Forwarder forwards one event through the active Runner event loop. A
// successful return means the event has been processed by that loop, including
// its session persistence and caller-visible emission steps.
type Forwarder func(context.Context, *event.Event) error

type holder struct {
	mu sync.RWMutex
	fn Forwarder
}

func (h *holder) set(fn Forwarder) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fn = fn
}

func (h *holder) get() Forwarder {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.fn
}

func (h *holder) clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fn = nil
}

// Attach binds an event forwarder to inv. Invocation clones retain the holder
// so nested agent execution can use the same Runner event loop.
func Attach(inv *agent.Invocation, fn Forwarder) {
	if inv == nil {
		return
	}
	if h, ok := agent.GetStateValue[*holder](inv, StateKeyForwardEvent); ok && h != nil {
		h.set(fn)
		return
	}
	inv.SetState(StateKeyForwardEvent, &holder{fn: fn})
}

// Invoke forwards evt when a Runner forwarder is available. The boolean result
// reports whether a forwarder was attached.
func Invoke(ctx context.Context, inv *agent.Invocation, evt *event.Event) (bool, error) {
	h, ok := agent.GetStateValue[*holder](inv, StateKeyForwardEvent)
	if !ok || h == nil {
		return false, nil
	}
	fn := h.get()
	if fn == nil {
		return false, nil
	}
	return true, fn(ctx, evt)
}

// Clear disables any forwarder attached to inv.
func Clear(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	if h, ok := agent.GetStateValue[*holder](inv, StateKeyForwardEvent); ok && h != nil {
		h.clear()
	}
	inv.DeleteState(StateKeyForwardEvent)
}
