//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sessionroute stores internal session-routing controls for runner
// persistence.
package sessionroute

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	stateKey                    = "__trpc_agent_session_route__"
	currentTurnRouteStatePrefix = "__trpc_agent_current_turn_session_route:"
)

// EventRouter routes an event to a non-root session for persistence.
type EventRouter interface {
	RouteEvent(
		root *agent.Invocation,
		routeEvt *event.Event,
	) (*session.Session, bool)
}

type currentTurnRoute struct {
	TargetAgentName string `json:"targetAgentName"`
	SessionID       string `json:"sessionID"`
}

type controller struct {
	mu      sync.Mutex
	routers []EventRouter
}

// CurrentTurnRouteState returns the root-session state update for a current
// turn session route.
func CurrentTurnRouteState(
	ownerAgentName string,
	targetAgentName string,
	root *session.Session,
	target *session.Session,
) (session.StateMap, error) {
	stateKey := currentTurnRouteStateKey(ownerAgentName)
	if root == nil || target == nil || sameSession(root, target) {
		return session.StateMap{stateKey: nil}, nil
	}
	route := currentTurnRoute{
		TargetAgentName: strings.TrimSpace(targetAgentName),
		SessionID:       strings.TrimSpace(target.ID),
	}
	if strings.TrimSpace(ownerAgentName) == "" ||
		route.TargetAgentName == "" ||
		route.SessionID == "" {
		return session.StateMap{stateKey: nil}, nil
	}
	raw, err := json.Marshal(route)
	if err != nil {
		return nil, err
	}
	return session.StateMap{stateKey: raw}, nil
}

// ApplyCurrentTurnRouteState applies a current-turn route state update to root.
func ApplyCurrentTurnRouteState(root *session.Session, state session.StateMap) {
	if root == nil {
		return
	}
	for key, value := range state {
		root.SetState(key, value)
	}
}

// HasCurrentTurnRoute reports whether root stores a current-turn route.
func HasCurrentTurnRoute(ownerAgentName string, root *session.Session) bool {
	if root == nil {
		return false
	}
	raw, ok := root.GetState(currentTurnRouteStateKey(ownerAgentName))
	return ok && len(raw) > 0
}

// ResolveCurrentTurnSession returns the session that should receive this user
// turn before the selected agent runs.
func ResolveCurrentTurnSession(
	ctx context.Context,
	service session.Service,
	root *session.Session,
	owner agent.Agent,
) (*session.Session, error) {
	if root == nil {
		return nil, nil
	}
	if owner == nil {
		return root, nil
	}
	raw, ok := root.GetState(currentTurnRouteStateKey(owner.Info().Name))
	if !ok || len(raw) == 0 {
		return root, nil
	}
	var route currentTurnRoute
	if err := json.Unmarshal(raw, &route); err != nil {
		return nil, err
	}
	targetAgentName := strings.TrimSpace(route.TargetAgentName)
	if targetAgentName == "" || owner.FindSubAgent(targetAgentName) == nil {
		return root, nil
	}
	sessionID := strings.TrimSpace(route.SessionID)
	if sessionID == "" || sessionID == root.ID {
		return root, nil
	}
	if service == nil {
		return nil, errors.New("session service is nil")
	}
	key := session.Key{
		AppName:   root.AppName,
		UserID:    root.UserID,
		SessionID: sessionID,
	}
	sess, err := service.GetSession(ctx, key)
	if err != nil {
		return nil, err
	}
	if sess != nil {
		return sess, nil
	}
	return service.CreateSession(ctx, key, session.StateMap{})
}

// AttachEventRouter attaches an internal event persistence router to the
// invocation and its ancestors.
func AttachEventRouter(inv *agent.Invocation, router EventRouter) {
	if inv == nil || router == nil {
		return
	}
	for current := inv; current != nil; current = current.GetParentInvocation() {
		attachEventRouter(current, router)
	}
}

// RouteEvent asks attached routers for event persistence decisions.
func RouteEvent(
	inv *agent.Invocation,
	routeEvt *event.Event,
) (*session.Session, bool) {
	ctrl, ok := controllerFor(inv)
	if !ok {
		return nil, false
	}
	routers := ctrl.eventRouters()
	for _, router := range routers {
		if router == nil {
			continue
		}
		if sess, ok := router.RouteEvent(inv, routeEvt); ok && sess != nil {
			return sess, true
		}
	}
	return nil, false
}

// SnapshotEventIdentity copies the event fields used for route lookup before
// plugins can replace the event.
func SnapshotEventIdentity(src *event.Event) *event.Event {
	if src == nil {
		return nil
	}
	return &event.Event{
		RequestID:          src.RequestID,
		InvocationID:       src.InvocationID,
		ParentInvocationID: src.ParentInvocationID,
		Branch:             src.Branch,
		FilterKey:          src.FilterKey,
	}
}

func attachEventRouter(inv *agent.Invocation, router EventRouter) {
	ctrl := getOrCreateController(inv)
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	ctrl.routers = append(ctrl.routers, nil)
	copy(ctrl.routers[1:], ctrl.routers)
	ctrl.routers[0] = router
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

func (c *controller) eventRouters() []EventRouter {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.routers) == 0 {
		return nil
	}
	routers := make([]EventRouter, len(c.routers))
	copy(routers, c.routers)
	return routers
}

func currentTurnRouteStateKey(ownerAgentName string) string {
	return currentTurnRouteStatePrefix + strings.TrimSpace(ownerAgentName)
}

func sameSession(a *session.Session, b *session.Session) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.AppName == b.AppName && a.UserID == b.UserID && a.ID == b.ID
}
