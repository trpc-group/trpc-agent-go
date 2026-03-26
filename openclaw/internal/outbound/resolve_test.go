//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package outbound

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestResolveTarget_ExplicitSessionID(t *testing.T) {
	target, err := ResolveTarget(
		context.Background(),
		DeliveryTarget{Target: "telegram:dm:123"},
	)
	require.NoError(t, err)
	require.Equal(t, "telegram", target.Channel)
	require.Equal(t, "123", target.Target)
}

func TestResolveTarget_DMSessionWithSuffix(t *testing.T) {
	t.Parallel()

	target, err := ResolveTarget(
		context.Background(),
		DeliveryTarget{
			Target: "telegram:dm:123:session-abc",
		},
	)
	require.NoError(t, err)
	require.Equal(t, DeliveryTarget{
		Channel: "telegram",
		Target:  "123",
	}, target)
}

func TestResolveTarget_WeComSessionTarget(t *testing.T) {
	t.Parallel()

	target, err := ResolveTarget(
		context.Background(),
		DeliveryTarget{
			Target: "wecom:thread:wecom:chat:chat-1",
		},
	)
	require.NoError(t, err)
	require.Equal(t, DeliveryTarget{
		Channel: "wecom",
		Target:  "group:chat-1",
	}, target)
}

func TestResolveTarget_WeComScopedGroupSession(t *testing.T) {
	t.Parallel()

	target, err := ResolveTarget(
		context.Background(),
		DeliveryTarget{
			Target: "wecom:chat:chat-1:user:user-1",
		},
	)
	require.NoError(t, err)
	require.Equal(t, DeliveryTarget{
		Channel: "wecom",
		Target:  "group:chat-1",
	}, target)
}

func TestResolveTarget_ExplicitWeComTarget(t *testing.T) {
	t.Parallel()

	target, err := ResolveTarget(
		context.Background(),
		DeliveryTarget{
			Channel: "wecom",
			Target:  "wecom:thread:wecom:dm:user-1",
		},
	)
	require.NoError(t, err)
	require.Equal(t, DeliveryTarget{
		Channel: "wecom",
		Target:  "single:user-1",
	}, target)
}

func TestResolveTarget_RejectsInvalidWeComTarget(t *testing.T) {
	t.Parallel()

	_, err := ResolveTarget(
		context.Background(),
		DeliveryTarget{
			Channel: "wecom",
			Target:  "wecom:thread:unknown",
		},
	)
	require.ErrorContains(
		t,
		err,
		"outbound: invalid target for wecom",
	)
}

func TestResolveTarget_RuntimeStateFallback(t *testing.T) {
	ctx := invocationCtx(
		t,
		"unknown",
		agent.RunOptions{
			RuntimeState: RuntimeStateForTarget(DeliveryTarget{
				Channel: "telegram",
				Target:  "456",
			}),
		},
	)

	target, err := ResolveTarget(ctx, DeliveryTarget{})
	require.NoError(t, err)
	require.Equal(t, DeliveryTarget{
		Channel: "telegram",
		Target:  "456",
	}, target)
}

func TestResolveTarget_SessionFallback(t *testing.T) {
	ctx := invocationCtx(t, "telegram:thread:999:topic:7", agent.RunOptions{})

	target, err := ResolveTarget(ctx, DeliveryTarget{})
	require.NoError(t, err)
	require.Equal(t, DeliveryTarget{
		Channel: "telegram",
		Target:  "999:topic:7",
	}, target)
}

func TestResolveTarget_WeComSessionFallback(t *testing.T) {
	t.Parallel()

	ctx := invocationCtx(
		t,
		"wecom:chat:chat-1:user:user-1",
		agent.RunOptions{},
	)

	target, err := ResolveTarget(ctx, DeliveryTarget{})
	require.NoError(t, err)
	require.Equal(t, DeliveryTarget{
		Channel: "wecom",
		Target:  "group:chat-1",
	}, target)
}

func TestResolveTarget_ErrorWhenUnavailable(t *testing.T) {
	_, err := ResolveTarget(context.Background(), DeliveryTarget{})
	require.Error(t, err)
}

func invocationCtx(
	t *testing.T,
	sessionID string,
	runOpts agent.RunOptions,
) context.Context {
	t.Helper()

	inv := agent.NewInvocation(
		agent.WithInvocationSession(
			session.NewSession("app", "user", sessionID),
		),
		agent.WithInvocationRunOptions(runOpts),
	)
	return agent.NewInvocationContext(context.Background(), inv)
}
