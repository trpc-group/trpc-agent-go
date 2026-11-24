//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"errors"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

type activeTurn struct {
	ctx          context.Context
	invocation   *agent.Invocation
	cancel       context.CancelFunc
	updates      chan acpsdk.SessionNotification
	overflow     chan struct{}
	overflowOnce sync.Once
	failureMu    sync.Mutex
	failure      error
}

const defaultSessionUpdateQueueSize = 64

var errSessionUpdateOverflow = errors.New("acpagent: session update queue overflow")

type activeTurnRegistry struct {
	mu    sync.RWMutex
	turns map[acpsdk.SessionId]*activeTurn
}

func newActiveTurnRegistry() *activeTurnRegistry {
	return &activeTurnRegistry{
		turns: make(map[acpsdk.SessionId]*activeTurn),
	}
}

func newActiveTurn(
	ctx context.Context,
	invocation *agent.Invocation,
	cancel context.CancelFunc,
) *activeTurn {
	return &activeTurn{
		ctx:        ctx,
		invocation: invocation,
		cancel:     cancel,
		updates:    make(chan acpsdk.SessionNotification, defaultSessionUpdateQueueSize),
		overflow:   make(chan struct{}),
	}
}

func (t *activeTurn) enqueue(notification acpsdk.SessionNotification) error {
	if t == nil {
		return nil
	}
	select {
	case t.updates <- notification:
		return nil
	default:
		t.overflowOnce.Do(func() {
			close(t.overflow)
			t.fail(errSessionUpdateOverflow)
		})
		return errSessionUpdateOverflow
	}
}

func (t *activeTurn) fail(err error) {
	if t == nil {
		return
	}
	t.failureMu.Lock()
	if t.failure == nil && err != nil {
		t.failure = err
	}
	t.failureMu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
}

func (t *activeTurn) failureError() error {
	if t == nil {
		return nil
	}
	t.failureMu.Lock()
	defer t.failureMu.Unlock()
	return t.failure
}

func (t *activeTurn) overflowed() <-chan struct{} {
	if t == nil {
		return nil
	}
	return t.overflow
}

func (t *activeTurn) hasOverflowed() bool {
	if t == nil {
		return false
	}
	select {
	case <-t.overflow:
		return true
	default:
		return false
	}
}

func (r *activeTurnRegistry) set(sessionID acpsdk.SessionId, turn *activeTurn) {
	if r == nil || turn == nil {
		return
	}
	r.mu.Lock()
	r.turns[sessionID] = turn
	r.mu.Unlock()
}

func (r *activeTurnRegistry) clear(sessionID acpsdk.SessionId, turn *activeTurn) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.turns[sessionID] == turn {
		delete(r.turns, sessionID)
	}
	r.mu.Unlock()
}

func (r *activeTurnRegistry) get(sessionID acpsdk.SessionId) (*activeTurn, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	turn, ok := r.turns[sessionID]
	r.mu.RUnlock()
	return turn, ok
}

func (r *activeTurnRegistry) failAll(err error) {
	if r == nil {
		return
	}
	r.mu.RLock()
	turns := make([]*activeTurn, 0, len(r.turns))
	for _, turn := range r.turns {
		turns = append(turns, turn)
	}
	r.mu.RUnlock()
	for _, turn := range turns {
		turn.fail(err)
	}
}
