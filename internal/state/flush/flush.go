//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package flush provides internal utilities for coordinating session flush requests between the runner and dependent
// components (e.g., AgentTool).It stores a flush function on the Invocation state so that callers can request a flush.
package flush

import (
	"context"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// StateKeyFlushSession is the invocation state key used by flush.Attach.
const StateKeyFlushSession = "__flush_session__"

// FlushRequest represents a single session flush request.
// The runner will close ACK when it has appended all events that were enqueued before this request was processed.
type FlushRequest struct {
	ACK chan struct{} // ACK is closed by the runner when the flush is complete.
}

// Flusher defines a flush function used to synchronize session events.
type flusher func(context.Context) error

// flusherHolder holds a shared flusher so Clear can invalidate it for all clones.
type flusherHolder struct {
	mu sync.RWMutex
	fn flusher
}

func (h *flusherHolder) set(fn flusher) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fn = fn
}

func (h *flusherHolder) get() flusher {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.fn
}

func (h *flusherHolder) clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fn = nil
}

// IsAttached reports whether a flush function has been attached to the invocation.
func IsAttached(inv *agent.Invocation) bool {
	holder, ok := agent.GetStateValue[*flusherHolder](inv, StateKeyFlushSession)
	if !ok || holder == nil {
		return false
	}
	return holder.get() != nil
}

// Attach binds a flush function to the given invocation and wires it to the provided flush channel.
// When Invoke is called, the function will enqueue a FlushRequest on ch and wait for ACK to be closed by the runner.
func Attach(ctx context.Context, inv *agent.Invocation, ch chan *FlushRequest) {
	var fn flusher = func(ctx context.Context) error {
		req := &FlushRequest{ACK: make(chan struct{})}
		// Enqueue the flush request on the flush channel.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ch <- req:
		}
		// Wait for the ACK to be closed by the runner.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-req.ACK:
			return nil
		}
	}
	// Reuse existing holder if present; otherwise create one.
	if holder, ok := agent.GetStateValue[*flusherHolder](inv, StateKeyFlushSession); ok && holder != nil {
		holder.set(fn)
	} else {
		inv.SetState(StateKeyFlushSession, &flusherHolder{fn: fn})
	}
}

// Invoke executes the flush function stored on the invocation state if present.
func Invoke(ctx context.Context, inv *agent.Invocation) error {
	holder, ok := agent.GetStateValue[*flusherHolder](inv, StateKeyFlushSession)
	if !ok || holder == nil {
		return nil
	}
	fn := holder.get()
	if fn == nil {
		return nil
	}
	return fn(ctx)
}

// Clear removes any flush function stored on the invocation state.
// This is intended to be called by the runner when the event loop finishes.
func Clear(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	if holder, ok := agent.GetStateValue[*flusherHolder](inv, StateKeyFlushSession); ok && holder != nil {
		holder.clear()
	}
	inv.DeleteState(StateKeyFlushSession)
}
