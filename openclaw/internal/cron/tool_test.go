//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package cron

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestToolAddSupportsAliasesAndCurrentChat(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
	)
	require.NoError(t, err)

	tool := NewTool(svc)
	ctx := agent.NewInvocationContext(
		context.Background(),
		&agent.Invocation{
			Session: &session.Session{
				ID:     "telegram:dm:12345",
				UserID: "user-1",
			},
		},
	)

	args, err := json.Marshal(map[string]any{
		"action":        "add",
		"task":          "collect system resources",
		"schedule_kind": "every",
		"interval":      "1m",
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	job, ok := result.(*Job)
	require.True(t, ok)
	require.Equal(t, "collect system resources", job.Message)
	require.Equal(t, ScheduleKindEvery, job.Schedule.Kind)
	require.Equal(t, "1m", job.Schedule.Every)
	require.Equal(t, "telegram", job.Delivery.Channel)
	require.Equal(t, "12345", job.Delivery.Target)
}

func TestToolAddSupportsRunAtAlias(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
	)
	require.NoError(t, err)

	tool := NewTool(svc)
	ctx := agent.NewInvocationContext(
		context.Background(),
		&agent.Invocation{
			Session: &session.Session{
				ID:     "telegram:dm:12345",
				UserID: "user-1",
			},
		},
	)

	args, err := json.Marshal(map[string]any{
		"action":        "add",
		"prompt":        "send a reminder",
		"schedule_kind": "at",
		"run_at":        "2026-03-07T09:00:00+08:00",
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	job, ok := result.(*Job)
	require.True(t, ok)
	require.Equal(t, "send a reminder", job.Message)
	require.Equal(t, ScheduleKindAt, job.Schedule.Kind)
	require.Equal(t, "2026-03-07T09:00:00+08:00", job.Schedule.At)
}
