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

func TestSentTextRecorderNormalizesOpaqueTarget(t *testing.T) {
	t.Parallel()

	recorder := NewSentTextRecorder()
	recorder.Record(DeliveryTarget{
		Target: "wecom:thread:wecom:dm:user-1",
	}, "hello")

	require.True(t, recorder.Contains(DeliveryTarget{
		Channel: "wecom",
		Target:  "single:user-1",
	}, "hello"))
	require.True(t, recorder.ContainsTarget(DeliveryTarget{
		Channel: "wecom",
		Target:  "single:user-1",
	}))
	require.False(t, recorder.Contains(DeliveryTarget{
		Channel: "wecom",
		Target:  "single:user-1",
	}, " hello "))
	require.False(t, recorder.ContainsTarget(DeliveryTarget{
		Channel: "wecom",
		Target:  "single:user-2",
	}))
	require.False(t, recorder.Contains(DeliveryTarget{
		Channel: "wecom",
		Target:  "single:user-2",
	}, "hello"))
}

func TestSentTextRecorderHandlesEmptyAndNilInputs(t *testing.T) {
	t.Parallel()

	var nilRecorder *SentTextRecorder
	nilRecorder.Record(DeliveryTarget{
		Channel: "telegram",
		Target:  "100",
	}, "hello")
	require.False(t, nilRecorder.Contains(DeliveryTarget{
		Channel: "telegram",
		Target:  "100",
	}, "hello"))
	require.False(t, nilRecorder.ContainsTarget(DeliveryTarget{
		Channel: "telegram",
		Target:  "100",
	}))

	recorder := &SentTextRecorder{}
	recorder.Record(DeliveryTarget{
		Channel: "telegram",
		Target:  "100",
	}, "hello")
	require.True(t, recorder.Contains(DeliveryTarget{
		Channel: "telegram",
		Target:  "100",
	}, "hello"))
	require.True(t, recorder.ContainsTarget(DeliveryTarget{
		Channel: "telegram",
		Target:  "100",
	}))
	require.False(t, recorder.Contains(DeliveryTarget{
		Channel: "telegram",
		Target:  "100",
	}, " "))
	require.False(t, recorder.ContainsTarget(DeliveryTarget{
		Channel: "telegram",
	}))
	require.False(t, recorder.Contains(DeliveryTarget{
		Channel: "telegram",
	}, "hello"))
}

func TestWithSentTextRecorderSkipsNil(t *testing.T) {
	t.Parallel()

	ctx := WithSentTextRecorder(context.Background(), nil)
	_, ok := sentTextRecorderFromContext(ctx)
	require.False(t, ok)

	recorder := NewSentTextRecorder()
	ctx = WithSentTextRecorder(nil, recorder)
	got, ok := sentTextRecorderFromContext(ctx)
	require.True(t, ok)
	require.Same(t, recorder, got)
}

func TestSentTextRecorderFromContextHandlesMissingValues(t *testing.T) {
	t.Parallel()

	_, ok := sentTextRecorderFromContext(nil)
	require.False(t, ok)

	_, ok = sentTextRecorderFromContext(context.Background())
	require.False(t, ok)

	recorder := NewSentTextRecorder()
	ctx := WithSentTextRecorder(invocationCtx(
		t,
		"cron:job-1:1",
		agent.RunOptions{},
	), recorder)
	got, ok := sentTextRecorderFromContext(ctx)
	require.True(t, ok)
	require.Same(t, recorder, got)
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
