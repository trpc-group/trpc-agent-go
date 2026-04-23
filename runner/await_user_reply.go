//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func (r *runner) applyAwaitUserReplyRoute(
	ctx context.Context,
	key session.Key,
	sess *session.Session,
	message model.Message,
	ro agent.RunOptions,
) (agent.RunOptions, error) {
	if r == nil || !r.awaitUserReplyRouting {
		return ro, nil
	}
	if ro.Agent != nil || ro.AgentByName != "" {
		return ro, nil
	}
	if message.Role != model.RoleUser {
		return ro, nil
	}

	route, ok, err := agent.PendingAwaitUserReplyRoute(sess)
	if err != nil {
		if clearErr := r.clearAwaitUserReplyRoute(ctx, key, sess); clearErr != nil {
			return ro, fmt.Errorf(
				"runner: clear invalid await_user_reply route: %w",
				clearErr,
			)
		}
		log.Warnf("runner: ignore invalid await_user_reply route: %v", err)
		return ro, nil
	}
	if !ok {
		return ro, nil
	}
	if !r.canResolveAgent(route.AgentName) {
		if clearErr := r.clearAwaitUserReplyRoute(ctx, key, sess); clearErr != nil {
			return ro, fmt.Errorf(
				"runner: clear stale await_user_reply route: %w",
				clearErr,
			)
		}
		log.Warnf(
			"runner: ignore stale await_user_reply route for agent %q",
			route.AgentName,
		)
		return ro, nil
	}
	if err := r.clearAwaitUserReplyRoute(ctx, key, sess); err != nil {
		return ro, fmt.Errorf(
			"runner: consume await_user_reply route: %w",
			err,
		)
	}
	ro.AgentByName = route.AgentName
	return ro, nil
}

func (r *runner) clearAwaitUserReplyRoute(
	ctx context.Context,
	key session.Key,
	sess *session.Session,
) error {
	if r == nil || r.sessionService == nil {
		return nil
	}
	state := agent.ClearAwaitUserReplyRouteState()
	if err := r.sessionService.UpdateSessionState(ctx, key, state); err != nil {
		return err
	}
	if sess == nil {
		return nil
	}
	for stateKey := range state {
		sess.SetState(stateKey, nil)
	}
	return nil
}

func (r *runner) canResolveAgent(name string) bool {
	if r == nil || name == "" {
		return false
	}
	if ag, ok := r.agents[name]; ok && ag != nil {
		return true
	}
	if factory, ok := r.agentFactories[name]; ok && factory != nil {
		return true
	}
	_, ok := findUniqueNestedAgent(r.agents, name)
	return ok
}
