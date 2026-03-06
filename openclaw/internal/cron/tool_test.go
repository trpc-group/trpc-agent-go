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
	"time"

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

func TestToolAddInfersScheduleKindFromEvery(t *testing.T) {
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
		"action":   "add",
		"message":  "collect cpu",
		"interval": "1m",
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	job, ok := result.(*Job)
	require.True(t, ok)
	require.Equal(t, ScheduleKindEvery, job.Schedule.Kind)
	require.Equal(t, "1m", job.Schedule.Every)
}

func TestToolListScopesToCurrentUser(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
	)
	require.NoError(t, err)

	_, err = svc.Add(&Job{
		Name:    "mine",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "mine",
		UserID:  "user-1",
	})
	require.NoError(t, err)
	_, err = svc.Add(&Job{
		Name:    "other",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "other",
		UserID:  "user-2",
	})
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

	result, err := tool.Call(ctx, []byte(`{"action":"list"}`))
	require.NoError(t, err)

	payload, ok := result.(map[string]any)
	require.True(t, ok)
	jobs, ok := payload["jobs"].([]*Job)
	require.True(t, ok)
	require.Len(t, jobs, 1)
	require.Equal(t, "mine", jobs[0].Name)
}

func TestToolRemoveRejectsOtherUsersJob(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
	)
	require.NoError(t, err)

	job, err := svc.Add(&Job{
		Name:    "other",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "other",
		UserID:  "user-2",
	})
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
		"action": "remove",
		"job_id": job.ID,
	})
	require.NoError(t, err)

	_, err = tool.Call(ctx, args)
	require.Error(t, err)
	require.Equal(t, "cron: unknown job: "+job.ID, err.Error())
}

func TestToolClearRemovesOnlyCurrentUserJobs(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
	)
	require.NoError(t, err)

	_, err = svc.Add(&Job{
		Name:    "mine",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "mine",
		UserID:  "user-1",
	})
	require.NoError(t, err)
	_, err = svc.Add(&Job{
		Name:    "other",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "other",
		UserID:  "user-2",
	})
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

	result, err := tool.Call(ctx, []byte(`{"action":"clear"}`))
	require.NoError(t, err)

	payload, ok := result.(map[string]any)
	require.True(t, ok)
	require.Equal(t, 1, payload["removed"])
	require.Len(t, svc.ListForUser("user-1", outbound.DeliveryTarget{}), 0)
	require.Len(t, svc.ListForUser("user-2", outbound.DeliveryTarget{}), 1)
}

func TestToolScheduledRunCannotMutateJobs(t *testing.T) {
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
				ID:     "cron:job:1",
				UserID: "user-1",
			},
			RunOptions: agent.RunOptions{
				RuntimeState: map[string]any{
					runtimeStateScheduledRun: true,
					runtimeStateJobID:        "job-1",
				},
			},
		},
	)

	args := []byte(`{"action":"add","message":"x","every":"1m"}`)
	_, err = tool.Call(ctx, args)
	require.Error(t, err)
	require.Equal(t, "cron: scheduled runs cannot add jobs", err.Error())
}

func TestToolListFiltersByTarget(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)
	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
		WithClock(func() time.Time { return now }),
	)
	require.NoError(t, err)

	_, err = svc.Add(&Job{
		Name:    "chat-1",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "mine",
		UserID:  "user-1",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "12345",
		},
	})
	require.NoError(t, err)
	_, err = svc.Add(&Job{
		Name:    "chat-2",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "mine",
		UserID:  "user-1",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "999",
		},
	})
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

	result, err := tool.Call(
		ctx,
		[]byte(`{"action":"list","channel":"telegram","target":"12345"}`),
	)
	require.NoError(t, err)

	payload, ok := result.(map[string]any)
	require.True(t, ok)
	jobs, ok := payload["jobs"].([]*Job)
	require.True(t, ok)
	require.Len(t, jobs, 1)
	require.Equal(t, "chat-1", jobs[0].Name)
}
