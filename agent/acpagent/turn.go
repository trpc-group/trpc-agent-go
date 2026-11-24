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
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

type activeTurn struct {
	ctx        context.Context
	invocation *agent.Invocation
}

type activeTurnRegistry struct {
	mu    sync.RWMutex
	turns map[acpsdk.SessionId]*activeTurn
}

func newActiveTurnRegistry() *activeTurnRegistry {
	return &activeTurnRegistry{
		turns: make(map[acpsdk.SessionId]*activeTurn),
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
