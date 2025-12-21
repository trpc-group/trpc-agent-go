//
// Tencent is pleased to support the open source community by making trpc-agent-go
// available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package appender provides internal utilities for appending session events from
// components that run outside of the Runner event loop (for example, AgentTool
// callable execution). It stores an append function on the Invocation state so
// callers can request a session-service append when available.
package appender

import (
	"context"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

// StateKeyAppendEvent is the invocation state key used by Attach.
const StateKeyAppendEvent = "__append_event__"

// Appender defines an append function used to persist events.
type Appender func(context.Context, *event.Event) error

// appenderHolder holds a shared appender so Clear can invalidate it for all
// clones.
type appenderHolder struct {
	mu sync.RWMutex
	fn Appender
}

func (h *appenderHolder) set(fn Appender) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fn = fn
}

func (h *appenderHolder) get() Appender {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.fn
}

func (h *appenderHolder) clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fn = nil
}

// IsAttached reports whether an appender has been attached to the invocation.
func IsAttached(inv *agent.Invocation) bool {
	holder, ok := agent.GetStateValue[*appenderHolder](
		inv, StateKeyAppendEvent,
	)
	if !ok || holder == nil {
		return false
	}
	return holder.get() != nil
}

// Attach binds an appender to the given invocation.
func Attach(inv *agent.Invocation, fn Appender) {
	if inv == nil {
		return
	}
	if holder, ok := agent.GetStateValue[*appenderHolder](
		inv, StateKeyAppendEvent,
	); ok && holder != nil {
		holder.set(fn)
		return
	}
	inv.SetState(StateKeyAppendEvent, &appenderHolder{fn: fn})
}

// Invoke executes the appender stored on the invocation state if present.
// The boolean return value indicates whether an appender was attached.
func Invoke(
	ctx context.Context,
	inv *agent.Invocation,
	evt *event.Event,
) (bool, error) {
	holder, ok := agent.GetStateValue[*appenderHolder](
		inv, StateKeyAppendEvent,
	)
	if !ok || holder == nil {
		return false, nil
	}
	fn := holder.get()
	if fn == nil {
		return false, nil
	}
	return true, fn(ctx, evt)
}

// Clear removes any appender stored on the invocation state.
// This is intended to be called by the runner when the event loop finishes.
func Clear(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	if holder, ok := agent.GetStateValue[*appenderHolder](
		inv, StateKeyAppendEvent,
	); ok && holder != nil {
		holder.clear()
	}
	inv.DeleteState(StateKeyAppendEvent)
}
