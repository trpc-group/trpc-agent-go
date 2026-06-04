//
// Tencent is pleased to support the open source community by making trpc-agent-go
// available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package livesession exposes the live session pointer that the runner
// persists events to. Downstream components (notably AgentTool) need this
// pointer so that sub-agents read the same session their events are appended
// to. The function-call processor's parallel execution path clones the
// invocation session for state-delta isolation; without livesession the
// sub-agent would otherwise read a frozen snapshot and loop forever.
package livesession

import (
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// StateKeyLiveSession is the invocation state key used by Attach.
const StateKeyLiveSession = "__live_session__"

// holder stores the live session pointer behind a mutex so Clear can
// invalidate it for all clones that share the holder reference.
type holder struct {
	mu   sync.RWMutex
	sess *session.Session
}

func (h *holder) set(sess *session.Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sess = sess
}

func (h *holder) get() *session.Session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sess
}

func (h *holder) clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sess = nil
}

// Attach binds the live session pointer to the given invocation. Subsequent
// Get calls on the invocation (or any clone that inherits the same state)
// return this pointer.
func Attach(inv *agent.Invocation, sess *session.Session) {
	if inv == nil {
		return
	}
	if h, ok := agent.GetStateValue[*holder](inv, StateKeyLiveSession); ok && h != nil {
		h.set(sess)
		return
	}
	inv.SetState(StateKeyLiveSession, &holder{sess: sess})
}

// Get returns the live session pointer attached to the invocation, if any.
// The boolean return value reports whether an attachment was found.
func Get(inv *agent.Invocation) (*session.Session, bool) {
	h, ok := agent.GetStateValue[*holder](inv, StateKeyLiveSession)
	if !ok || h == nil {
		return nil, false
	}
	sess := h.get()
	if sess == nil {
		return nil, false
	}
	return sess, true
}

// Clear removes any live session attached to the invocation. The runner
// invokes this once the agent loop completes so cloned invocations do not
// keep dangling references to retired sessions.
func Clear(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	if h, ok := agent.GetStateValue[*holder](inv, StateKeyLiveSession); ok && h != nil {
		h.clear()
	}
	inv.DeleteState(StateKeyLiveSession)
}
