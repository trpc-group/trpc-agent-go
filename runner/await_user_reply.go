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
) (agent.RunOptions, string, string, session.StateMap, error) {
	if r == nil || !r.awaitUserReplyRouting {
		return ro, "", "", nil, nil
	}
	if message.Role != model.RoleUser {
		return ro, "", "", nil, nil
	}
	deferClear := ro.LatestTurnReplacement != nil
	if ro.Agent != nil || ro.AgentByName != "" {
		ro, rootName, clearState, err := r.clearOverriddenAwaitUserReplyRoute(
			ctx,
			key,
			sess,
			ro,
			deferClear,
		)
		return ro, rootName, "", clearState, err
	}

	route, ok, err := agent.PendingAwaitUserReplyRoute(sess)
	if err != nil {
		clearState, clearErr := r.clearAwaitUserReplyRoute(
			ctx, key, sess, deferClear,
		)
		if clearErr != nil {
			return ro, "", "", nil, fmt.Errorf(
				"runner: clear invalid await_user_reply route: %w",
				clearErr,
			)
		}
		log.Warnf("runner: ignore invalid await_user_reply route: %v", err)
		return ro, "", "", clearState, nil
	}
	if !ok {
		return ro, "", "", nil, nil
	}
	selected, rootName, ok, err := r.resolveAwaitUserReplyRoute(
		ctx,
		route,
		ro,
	)
	if err != nil {
		return ro, "", "", nil, err
	}
	if !ok {
		clearState, clearErr := r.clearAwaitUserReplyRoute(
			ctx, key, sess, deferClear,
		)
		if clearErr != nil {
			return ro, "", "", nil, fmt.Errorf(
				"runner: clear stale await_user_reply route: %w",
				clearErr,
			)
		}
		log.Warnf(
			"runner: ignore stale await_user_reply route for path %q",
			route.LookupPath,
		)
		return ro, "", "", clearState, nil
	}
	clearState, err := r.clearAwaitUserReplyRoute(ctx, key, sess, deferClear)
	if err != nil {
		return ro, "", "", nil, fmt.Errorf(
			"runner: consume await_user_reply route: %w",
			err,
		)
	}
	ro.Agent = selected
	return ro, rootName, route.LookupPath, clearState, nil
}

func (r *runner) clearOverriddenAwaitUserReplyRoute(
	ctx context.Context,
	key session.Key,
	sess *session.Session,
	ro agent.RunOptions,
	deferClear bool,
) (agent.RunOptions, string, session.StateMap, error) {
	_, ok, err := agent.PendingAwaitUserReplyRoute(sess)
	if err != nil {
		clearState, clearErr := r.clearAwaitUserReplyRoute(
			ctx, key, sess, deferClear,
		)
		if clearErr != nil {
			return ro, "", nil, fmt.Errorf(
				"runner: clear invalid await_user_reply route: %w",
				clearErr,
			)
		}
		log.Warnf("runner: ignore invalid await_user_reply route: %v", err)
		return ro, "", clearState, nil
	}
	if !ok {
		return ro, "", nil, nil
	}
	clearState, err := r.clearAwaitUserReplyRoute(ctx, key, sess, deferClear)
	if err != nil {
		return ro, "", nil, fmt.Errorf(
			"runner: clear overridden await_user_reply route: %w",
			err,
		)
	}
	return ro, "", clearState, nil
}

func (r *runner) clearAwaitUserReplyRoute(
	ctx context.Context,
	key session.Key,
	sess *session.Session,
	deferPersist bool,
) (session.StateMap, error) {
	if r == nil || r.sessionService == nil {
		return nil, nil
	}
	state := agent.ClearAwaitUserReplyRouteState()
	if !deferPersist {
		if err := r.sessionService.UpdateSessionState(ctx, key, state); err != nil {
			return nil, err
		}
	}
	if sess != nil {
		for stateKey := range state {
			sess.SetState(stateKey, nil)
		}
	}
	if deferPersist {
		return state, nil
	}
	return nil, nil
}
