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

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

const (
	// stateKeyFlushSession is the invocation state key used to store the flush function.
	stateKeyFlushSession = "__flush_session__"
)

// FlushRequest represents a single session flush request.
// The runner will close ACK when it has appended all events that were enqueued before this request was processed.
type FlushRequest struct {
	ACK chan struct{} // ACK is closed by the runner when the flush is complete.
}

type flusher func(context.Context) error

// Attach binds a flush function to the given invocation using its state map, wiring it to the provided flush channel.
// When Invoke is called, the function will enqueue a FlushRequest on ch and wait for ACK to be closed by the runner.
func Attach(inv *agent.Invocation, ch chan *FlushRequest) {
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
	inv.SetState(stateKeyFlushSession, fn)
}

// Invoke executes the flush function stored on the invocation state if present.
func Invoke(ctx context.Context, inv *agent.Invocation) error {
	fn, ok := agent.GetStateValue[flusher](inv, stateKeyFlushSession)
	if !ok || fn == nil {
		return nil
	}
	return fn(ctx)
}

// Clear removes any flush function stored on the invocation state.
// This is intended to be called by the runner when the event loop finishes.
func Clear(inv *agent.Invocation) {
	inv.DeleteState(stateKeyFlushSession)
}
