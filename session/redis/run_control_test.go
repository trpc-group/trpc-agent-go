//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redis

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/runcontrol"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestService_RunControlLifecycle(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithRunControlEnabled(true),
		WithRunLeaseTTL(5*time.Second),
	)
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	permit, err := svc.BeginRun(ctx, runcontrol.BeginRequest{
		SessionKey:   key,
		RequestID:    "req-1",
		InvocationID: "inv-1",
		AgentName:    "agent",
		NodeID:       "node-a",
		Policy:       runcontrol.PolicyRejectIfBusy,
		LeaseTTL:     5 * time.Second,
	})
	require.NoError(t, err)
	require.NotNil(t, permit)

	_, err = svc.BeginRun(ctx, runcontrol.BeginRequest{
		SessionKey:   key,
		RequestID:    "req-2",
		InvocationID: "inv-2",
		AgentName:    "agent",
		NodeID:       "node-b",
		Policy:       runcontrol.PolicyRejectIfBusy,
		LeaseTTL:     5 * time.Second,
	})
	require.ErrorIs(t, err, runcontrol.ErrRunBusy)

	renewed, err := svc.RenewRun(ctx, permit.Lease, 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, renewed)
	require.False(t, renewed.CancelRequested)

	err = svc.CancelRun(ctx, runcontrol.CancelRequest{
		SessionKey: key,
		Reason:     "newer request",
	})
	require.NoError(t, err)

	renewed, err = svc.RenewRun(ctx, permit.Lease, 5*time.Second)
	require.NoError(t, err)
	require.True(t, renewed.CancelRequested)
	require.Equal(t, "newer request", renewed.CancelReason)

	require.NoError(t, svc.FinishRun(ctx, permit.Lease, runcontrol.FinishRequest{
		Status: runcontrol.StateCanceled,
	}))

	permit2, err := svc.BeginRun(ctx, runcontrol.BeginRequest{
		SessionKey:   key,
		RequestID:    "req-2",
		InvocationID: "inv-2",
		AgentName:    "agent",
		NodeID:       "node-b",
		Policy:       runcontrol.PolicyRejectIfBusy,
		LeaseTTL:     5 * time.Second,
	})
	require.NoError(t, err)
	require.NotNil(t, permit2)
}
