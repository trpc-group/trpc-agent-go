//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package eventcontrol stores internal event handling controls.
package eventcontrol

import (
	"context"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

const stateKey = "__trpc_agent_event_control__"

// PersistenceHandler handles post-plugin event persistence before runner root persistence.
type PersistenceHandler interface {
	HandleEventPersistence(
		ctx context.Context,
		root *agent.Invocation,
		routeEvt *event.Event,
		evt *event.Event,
	) bool
}

type controller struct {
	mu              sync.Mutex
	skipInvocations map[string]struct{}
	handlers        []PersistenceHandler
}

// AttachPersistenceHandler attaches an internal event persistence handler to
// the invocation and its ancestors.
func AttachPersistenceHandler(inv *agent.Invocation, handler PersistenceHandler) {
	if inv == nil || handler == nil {
		return
	}
	for current := inv; current != nil; current = current.GetParentInvocation() {
		attachPersistenceHandler(current, handler)
	}
}

func attachPersistenceHandler(inv *agent.Invocation, handler PersistenceHandler) {
	ctrl := getOrCreateController(inv)
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	ctrl.handlers = append(ctrl.handlers, nil)
	copy(ctrl.handlers[1:], ctrl.handlers)
	ctrl.handlers[0] = handler
}

// HandlePersistence lets attached handlers process an event before root persistence.
func HandlePersistence(
	ctx context.Context,
	inv *agent.Invocation,
	routeEvt *event.Event,
	evt *event.Event,
) bool {
	ctrl, ok := controllerFor(inv)
	if !ok {
		return false
	}
	handlers := ctrl.persistenceHandlers()
	for _, handler := range handlers {
		if handler != nil && handler.HandleEventPersistence(ctx, inv, routeEvt, evt) {
			return true
		}
	}
	return false
}

// MarkSkipPersistence marks an event invocation as output-only for runner
// persistence. The skip scope is the invocation ID, not a single event ID, so
// all events with the same invocation ID are skipped for root persistence.
func MarkSkipPersistence(inv *agent.Invocation, evt *event.Event) {
	if inv == nil || evt == nil || evt.InvocationID == "" {
		return
	}
	ctrl := getOrCreateController(inv)
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if ctrl.skipInvocations == nil {
		ctrl.skipInvocations = make(map[string]struct{})
	}
	ctrl.skipInvocations[evt.InvocationID] = struct{}{}
}

// SkipPersistence reports whether an event should be emitted but not persisted.
func SkipPersistence(inv *agent.Invocation, evt *event.Event) bool {
	if inv == nil || evt == nil || evt.InvocationID == "" {
		return false
	}
	ctrl, ok := controllerFor(inv)
	if !ok {
		return false
	}
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	_, skip := ctrl.skipInvocations[evt.InvocationID]
	return skip
}

func getOrCreateController(inv *agent.Invocation) *controller {
	if ctrl, ok := controllerFor(inv); ok {
		return ctrl
	}
	ctrl := &controller{}
	inv.SetState(stateKey, ctrl)
	return ctrl
}

func controllerFor(inv *agent.Invocation) (*controller, bool) {
	if inv == nil {
		return nil, false
	}
	ctrl, ok := agent.GetStateValue[*controller](inv, stateKey)
	return ctrl, ok && ctrl != nil
}

func (c *controller) persistenceHandlers() []PersistenceHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.handlers) == 0 {
		return nil
	}
	handlers := make([]PersistenceHandler, len(c.handlers))
	copy(handlers, c.handlers)
	return handlers
}
