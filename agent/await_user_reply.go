//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"encoding/json"
	"errors"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	awaitUserReplyInvocationStateKey = "__await_user_reply_route__"
	awaitUserReplySessionStateKey    = "__trpc_agent_await_user_reply_route__"
	awaitUserReplyEventExtensionKey  = "trpc_agent.await_user_reply_route"
)

// AwaitUserReplyRoute describes which agent should receive the next user turn.
//
// Runner consumes this route once when await-user-reply routing is enabled.
type AwaitUserReplyRoute struct {
	// AgentName is the target agent that should resume on the next user turn.
	AgentName string `json:"agent_name"`
}

// MarkAwaitingUserReply marks the current invocation so its next terminal
// response persists a one-shot "resume here on the next user reply" route.
func MarkAwaitingUserReply(inv *Invocation) error {
	if inv == nil {
		return errors.New("invocation is nil")
	}
	route, err := normalizeAwaitUserReplyRoute(
		AwaitUserReplyRoute{AgentName: inv.AgentName},
	)
	if err != nil {
		return err
	}
	inv.SetState(awaitUserReplyInvocationStateKey, route)
	return nil
}

// CurrentAwaitUserReplyRoute returns the route currently staged on an
// invocation by MarkAwaitingUserReply.
func CurrentAwaitUserReplyRoute(
	inv *Invocation,
) (AwaitUserReplyRoute, bool) {
	route, ok := GetStateValue[AwaitUserReplyRoute](
		inv,
		awaitUserReplyInvocationStateKey,
	)
	if !ok {
		return AwaitUserReplyRoute{}, false
	}
	normalized, err := normalizeAwaitUserReplyRoute(route)
	if err != nil {
		return AwaitUserReplyRoute{}, false
	}
	return normalized, true
}

// PendingAwaitUserReplyRoute reads a persisted next-user-turn route from the
// session state.
func PendingAwaitUserReplyRoute(
	sess *session.Session,
) (AwaitUserReplyRoute, bool, error) {
	if sess == nil {
		return AwaitUserReplyRoute{}, false, nil
	}
	raw, ok := sess.GetState(awaitUserReplySessionStateKey)
	if !ok || len(raw) == 0 {
		return AwaitUserReplyRoute{}, false, nil
	}
	var route AwaitUserReplyRoute
	if err := json.Unmarshal(raw, &route); err != nil {
		return AwaitUserReplyRoute{}, false, err
	}
	normalized, err := normalizeAwaitUserReplyRoute(route)
	if err != nil {
		return AwaitUserReplyRoute{}, false, err
	}
	return normalized, true, nil
}

// ClearAwaitUserReplyRouteState returns the session update that clears the
// pending next-user-turn route.
func ClearAwaitUserReplyRouteState() session.StateMap {
	return session.StateMap{
		awaitUserReplySessionStateKey: nil,
	}
}

// State encodes the route into session state.
func (r AwaitUserReplyRoute) State() (session.StateMap, error) {
	normalized, err := normalizeAwaitUserReplyRoute(r)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	return session.StateMap{
		awaitUserReplySessionStateKey: raw,
	}, nil
}

// AttachEvent stores the route in one event's state delta and extensions.
func (r AwaitUserReplyRoute) AttachEvent(evt *event.Event) error {
	if evt == nil {
		return nil
	}
	state, err := r.State()
	if err != nil {
		return err
	}
	if evt.StateDelta == nil {
		evt.StateDelta = make(map[string][]byte, len(state))
	}
	for key, value := range state {
		evt.StateDelta[key] = value
	}
	return event.SetExtension(
		evt,
		awaitUserReplyEventExtensionKey,
		r,
	)
}

func attachAwaitUserReplyRoute(inv *Invocation, evt *event.Event) {
	if inv == nil || evt == nil || evt.Response == nil {
		return
	}
	if evt.Response.IsPartial || !evt.Response.Done || evt.IsError() {
		return
	}
	route, ok := CurrentAwaitUserReplyRoute(inv)
	if !ok {
		return
	}
	if err := route.AttachEvent(evt); err != nil {
		log.Warnf(
			"attach await_user_reply route failed for agent %q: %v",
			inv.AgentName,
			err,
		)
	}
}

func normalizeAwaitUserReplyRoute(
	route AwaitUserReplyRoute,
) (AwaitUserReplyRoute, error) {
	route.AgentName = strings.TrimSpace(route.AgentName)
	if route.AgentName == "" {
		return AwaitUserReplyRoute{}, errors.New(
			"await_user_reply requires a non-empty agent name",
		)
	}
	return route, nil
}
