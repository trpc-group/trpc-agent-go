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
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
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

func TestToolAddCapturesRuntimeProfile(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		nil,
	)
	require.NoError(t, err)

	tool := NewTool(svc)
	invCtx := agent.NewInvocationContext(
		context.Background(),
		&agent.Invocation{
			Session: &session.Session{
				ID:     "telegram:dm:12345",
				UserID: "user-1",
			},
		},
	)
	ctx := runtimeprofile.WithProfile(invCtx, runtimeprofile.Profile{
		ID:      "retail",
		Version: "v2",
		AppName: "retail-app",
	})
	ctx = runtimeprofile.WithRequest(ctx, runtimeprofile.Request{
		Channel:   "wecom",
		TenantID:  "tenant-a",
		SessionID: "session-a",
	})

	args, err := json.Marshal(map[string]any{
		"action":        "add",
		"task":          "collect system resources",
		"schedule_kind": "every",
		"interval":      "1m",
		"headless":      true,
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	job, ok := result.(*Job)
	require.True(t, ok)
	require.NotNil(t, job.Profile)
	require.Equal(t, "retail", job.Profile.ID)
	require.Equal(t, "v2", job.Profile.Version)
	require.Equal(t, "retail-app", job.Profile.AppName)
	require.Equal(t, "wecom", job.Profile.Channel)
	require.Equal(t, "tenant-a", job.Profile.TenantID)
	require.Equal(t, "session-a", job.Profile.SessionID)
}

func TestToolAddRejectsAnonymousRuntimeProfile(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		nil,
	)
	require.NoError(t, err)

	tool := NewTool(svc)
	invCtx := agent.NewInvocationContext(
		context.Background(),
		&agent.Invocation{
			Session: &session.Session{
				ID:     "telegram:dm:12345",
				UserID: "user-1",
			},
		},
	)
	ctx := runtimeprofile.WithProfile(invCtx, runtimeprofile.Profile{
		Prompt: runtimeprofile.Prompt{
			Instruction: "tenant instruction",
		},
	})

	args, err := json.Marshal(map[string]any{
		"action":        "add",
		"task":          "collect system resources",
		"schedule_kind": "every",
		"interval":      "1m",
		"headless":      true,
	})
	require.NoError(t, err)

	_, err = tool.Call(ctx, args)
	require.Error(t, err)
	require.Contains(t, err.Error(), errProfileIDRequired)

	ctxWhitespace := runtimeprofile.WithProfile(
		invCtx,
		runtimeprofile.Profile{
			ID: "   ",
			Prompt: runtimeprofile.Prompt{
				Instruction: "tenant instruction",
			},
		},
	)
	_, err = tool.Call(ctxWhitespace, args)
	require.Error(t, err)
	require.Contains(t, err.Error(), errProfileIDRequired)
}

func TestToolAddSupportsExecutionPolicyAndHeadless(t *testing.T) {
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
				ID:     "wecom:chat:group-1",
				UserID: "user-1",
			},
		},
	)

	args, err := json.Marshal(map[string]any{
		"action":         "add",
		"message":        "share golang tips",
		"schedule_kind":  "every",
		"every":          "1m",
		"max_runs":       5,
		"ends_at":        "2026-03-25T20:30:00Z",
		"overlap_policy": OverlapPolicyReplace,
		"headless":       true,
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	job, ok := result.(*Job)
	require.True(t, ok)
	require.Equal(t, 5, job.Policy.MaxRuns)
	require.NotNil(t, job.Policy.EndsAt)
	require.Equal(
		t,
		OverlapPolicyReplace,
		job.Policy.OverlapPolicy,
	)
	require.Empty(t, job.Delivery.Channel)
	require.Empty(t, job.Delivery.Target)
}

func TestToolAddNormalizesExplicitWeComTarget(t *testing.T) {
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
				ID:     "stdin:session",
				UserID: "user-1",
			},
		},
	)

	args, err := json.Marshal(map[string]any{
		"action":        "add",
		"message":       "share good news",
		"schedule_kind": "every",
		"every":         "10s",
		"channel":       "wecom",
		"target":        "wecom:thread:wecom:chat:chat-1",
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	job, ok := result.(*Job)
	require.True(t, ok)
	require.Equal(t, outbound.DeliveryTarget{
		Channel: "wecom",
		Target:  "group:chat-1",
	}, job.Delivery)
}

func TestToolAddRejectsInvalidExplicitWeComTarget(t *testing.T) {
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
				ID:     "stdin:session",
				UserID: "user-1",
			},
		},
	)

	args, err := json.Marshal(map[string]any{
		"action":        "add",
		"message":       "share good news",
		"schedule_kind": "every",
		"every":         "10s",
		"channel":       "wecom",
		"target":        "wecom:thread:unknown",
	})
	require.NoError(t, err)

	_, err = tool.Call(ctx, args)
	require.ErrorContains(
		t,
		err,
		"outbound: invalid target for wecom",
	)
}

func TestToolAddFailsWithoutResolvableDeliveryTarget(t *testing.T) {
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
				ID:     "stdin:session",
				UserID: "user-1",
			},
		},
	)

	_, err = tool.Call(
		ctx,
		[]byte(`{
			"action":"add",
			"message":"report status",
			"schedule_kind":"every",
			"every":"1m"
		}`),
	)
	require.ErrorContains(t, err, errDeliveryTargetUnavailable)
}

func TestToolAddSupportsRunAtAlias(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 7, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
		WithClock(func() time.Time { return now }),
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

func TestToolAddSupportsRelativeDelayOneShot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 15, 11, 3, 0, 0, time.FixedZone("CST", 8*60*60))
	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
		WithClock(func() time.Time { return now }),
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
		"action":  "add",
		"message": "send a reminder",
		"after":   "30m",
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	job, ok := result.(*Job)
	require.True(t, ok)
	expectedAt := now.Add(30 * time.Minute)
	require.Equal(t, ScheduleKindAt, job.Schedule.Kind)
	require.Equal(t, expectedAt.Format(time.RFC3339), job.Schedule.At)
	require.NotNil(t, job.NextRunAt)
	require.True(t, job.NextRunAt.Equal(expectedAt))
	require.Equal(t, "send a reminder", job.Message)
}

func TestToolAddSupportsMillisecondRelativeDelay(t *testing.T) {
	t.Parallel()

	now := time.Date(
		2026,
		time.May,
		15,
		11,
		3,
		0,
		int(250*time.Millisecond),
		time.FixedZone("CST", 8*60*60),
	)
	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
		WithClock(func() time.Time { return now }),
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

	delayMS := int64(1500)
	args, err := json.Marshal(map[string]any{
		"action":  "add",
		"message": "send a reminder",
		"delayMs": delayMS,
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	job, ok := result.(*Job)
	require.True(t, ok)
	expectedAt := now.Add(time.Duration(delayMS) * time.Millisecond)
	require.Equal(t, expectedAt.Format(time.RFC3339Nano), job.Schedule.At)
	require.NotNil(t, job.NextRunAt)
	require.True(t, job.NextRunAt.Equal(expectedAt))
}

func TestToolAddRejectsOverflowingMillisecondDelay(t *testing.T) {
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
		"message":  "send a reminder",
		"delay_ms": maxDelayMilliseconds + 1,
	})
	require.NoError(t, err)

	_, err = tool.Call(ctx, args)
	require.ErrorContains(t, err, errAfterMSDelayTooLarge)
}

func TestToolAddSupportsFractionalRelativeDelay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 15, 11, 3, 0, 0, time.FixedZone("CST", 8*60*60))
	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
		WithClock(func() time.Time { return now }),
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
		"action":  "add",
		"message": "send a reminder",
		"after":   "1.5s",
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	job, ok := result.(*Job)
	require.True(t, ok)
	expectedAt := now.Add(1500 * time.Millisecond)
	require.Equal(t, expectedAt.Format(time.RFC3339Nano), job.Schedule.At)
	require.NotNil(t, job.NextRunAt)
	require.True(t, job.NextRunAt.Equal(expectedAt))
}

func TestToolAddSupportsRelativeDelayAliases(t *testing.T) {
	t.Parallel()

	now := time.Date(
		2026,
		time.May,
		15,
		11,
		3,
		0,
		int(250*time.Millisecond),
		time.FixedZone("CST", 8*60*60),
	)
	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
		WithClock(func() time.Time { return now }),
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

	delayMS := int64(750)
	args, err := json.Marshal(map[string]any{
		"action":        "add",
		"message":       "send a reminder",
		"schedule_kind": ScheduleKindAfter,
		"afterMs":       delayMS,
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	job, ok := result.(*Job)
	require.True(t, ok)
	expectedAt := now.Add(time.Duration(delayMS) * time.Millisecond)
	require.Equal(t, ScheduleKindAt, job.Schedule.Kind)
	require.Equal(t, expectedAt.Format(time.RFC3339Nano), job.Schedule.At)
	require.NotNil(t, job.NextRunAt)
	require.True(t, job.NextRunAt.Equal(expectedAt))
}

func TestToolAddRejectsInvalidRelativeDelayInputs(t *testing.T) {
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

	const reminderMessage = "send a reminder"
	tests := []struct {
		name     string
		args     map[string]any
		errorMsg string
	}{
		{
			name: "duration and milliseconds",
			args: map[string]any{
				"after":    "30m",
				"after_ms": int64(1000),
			},
			errorMsg: "cron: after and after_ms cannot both be set",
		},
		{
			name: "duplicate millisecond aliases",
			args: map[string]any{
				"after_ms": int64(1000),
				"delayMs":  int64(2000),
			},
			errorMsg: "cron: only one after_ms alias can be set",
		},
		{
			name: "duplicate duration aliases",
			args: map[string]any{
				"after": "30m",
				"delay": "20m",
			},
			errorMsg: errAfterAliasConflict,
		},
		{
			name: "invalid duration",
			args: map[string]any{
				"after": "tomorrow",
			},
			errorMsg: "cron: invalid after delay",
		},
		{
			name: "zero duration",
			args: map[string]any{
				"delay": "0s",
			},
			errorMsg: "cron: after delay must be positive",
		},
		{
			name: "zero milliseconds",
			args: map[string]any{
				"after_ms": int64(0),
			},
			errorMsg: "cron: after_ms delay must be positive",
		},
		{
			name: "recurring schedule kind",
			args: map[string]any{
				"after":         "30m",
				"schedule_kind": ScheduleKindEvery,
			},
			errorMsg: "cron: schedule_kind must be empty, at, or after",
		},
		{
			name: "absolute schedule",
			args: map[string]any{
				"after": "30m",
				"at":    "2026-05-15T12:00:00+08:00",
			},
			errorMsg: errAfterWithSchedule,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := map[string]any{
				"action":  "add",
				"message": reminderMessage,
			}
			for key, value := range tt.args {
				args[key] = value
			}
			payload, err := json.Marshal(args)
			require.NoError(t, err)

			_, err = tool.Call(ctx, payload)
			require.ErrorContains(t, err, tt.errorMsg)
		})
	}
}

func TestToolAddKeepsPastRunAtCompatibility(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 15, 11, 3, 0, 0, time.FixedZone("CST", 8*60*60))
	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
		WithClock(func() time.Time { return now }),
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
		"action": "add",
		"prompt": "send a reminder",
		"run_at": "2026-05-15T09:52:25+08:00",
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	job, ok := result.(*Job)
	require.True(t, ok)
	pastAt, err := time.Parse(time.RFC3339, "2026-05-15T09:52:25+08:00")
	require.NoError(t, err)
	require.Equal(t, ScheduleKindAt, job.Schedule.Kind)
	require.Equal(t, pastAt.Format(time.RFC3339), job.Schedule.At)
	require.NotNil(t, job.NextRunAt)
	require.True(t, job.NextRunAt.Equal(pastAt))
}

func TestToolAddRejectsAfterWithRecurringSchedule(t *testing.T) {
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
		"action":  "add",
		"message": "send a reminder",
		"after":   "30m",
		"every":   "30m",
	})
	require.NoError(t, err)

	_, err = tool.Call(ctx, args)
	require.ErrorContains(t, err, errAfterWithSchedule)
}

func TestToolAddAllowsDelayWithTimezone(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 15, 11, 3, 0, 0, time.FixedZone("CST", 8*60*60))
	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
		WithClock(func() time.Time { return now }),
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
		"message":  "send a reminder",
		"delay":    "30m",
		"timezone": "Asia/Shanghai",
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	job, ok := result.(*Job)
	require.True(t, ok)
	require.Equal(t, ScheduleKindAt, job.Schedule.Kind)
	require.Empty(t, job.Schedule.Timezone)
	require.Equal(
		t,
		now.Add(30*time.Minute).Format(time.RFC3339),
		job.Schedule.At,
	)
}

func TestToolUpdateTimezonePreservesExistingSchedule(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
	)
	require.NoError(t, err)

	job, err := svc.Add(&Job{
		Name:    "mine",
		Enabled: true,
		Schedule: Schedule{
			Kind:     ScheduleKindCron,
			CronExpr: "0 9 * * *",
		},
		Message: "send a reminder",
		UserID:  "user-1",
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
		"action":   "update",
		"job_id":   job.ID,
		"timezone": "UTC",
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	updated, ok := result.(*Job)
	require.True(t, ok)
	require.Equal(t, ScheduleKindCron, updated.Schedule.Kind)
	require.Equal(t, "0 9 * * *", updated.Schedule.CronExpr)
	require.Equal(t, "UTC", updated.Schedule.Timezone)
}

func TestToolUpdateSupportsRelativeDelay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 15, 11, 3, 0, 0, time.FixedZone("CST", 8*60*60))
	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
		WithClock(func() time.Time { return now }),
	)
	require.NoError(t, err)

	job, err := svc.Add(&Job{
		Name:    "mine",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "send a reminder",
		UserID:  "user-1",
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
		"action": "update",
		"job_id": job.ID,
		"after":  "30m",
	})
	require.NoError(t, err)

	result, err := tool.Call(ctx, args)
	require.NoError(t, err)

	updated, ok := result.(*Job)
	require.True(t, ok)
	require.Equal(t, ScheduleKindAt, updated.Schedule.Kind)
	require.Equal(
		t,
		now.Add(30*time.Minute).Format(time.RFC3339),
		updated.Schedule.At,
	)
}

func TestToolUpdateRejectsInvalidRelativeDelay(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
	)
	require.NoError(t, err)

	job, err := svc.Add(&Job{
		Name:    "mine",
		Enabled: true,
		Schedule: Schedule{
			Kind:  ScheduleKindEvery,
			Every: "1m",
		},
		Message: "send a reminder",
		UserID:  "user-1",
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
		"action":        "update",
		"job_id":        job.ID,
		"after":         "30m",
		"schedule_kind": ScheduleKindEvery,
	})
	require.NoError(t, err)

	_, err = tool.Call(ctx, args)
	require.ErrorContains(
		t,
		err,
		"cron: schedule_kind must be empty, at, or after",
	)
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

func TestTool_StatusUpdateRunAndHelpers(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, svc.Close())
	})

	tool := NewTool(nil)
	tool.SetService(svc)
	require.Equal(t, toolCron, tool.Declaration().Name)

	ctx := agent.NewInvocationContext(
		context.Background(),
		&agent.Invocation{
			Session: &session.Session{
				ID:     "telegram:dm:12345",
				UserID: "user-1",
			},
		},
	)

	status, err := tool.Call(ctx, []byte(`{"action":"status"}`))
	require.NoError(t, err)
	require.NotNil(t, status)

	job, err := svc.Add(&Job{
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

	updated, err := tool.Call(ctx, []byte(
		`{"action":"update","job_id":"`+job.ID+`","name":"new"}`,
	))
	require.NoError(t, err)
	require.Equal(t, "new", updated.(*Job).Name)

	ran, err := tool.Call(ctx, []byte(
		`{"action":"run","job_id":"`+job.ID+`"}`,
	))
	require.NoError(t, err)
	require.Equal(t, job.ID, ran.(*Job).ID)

	require.Equal(t, "message", resolveMessage(toolInput{
		Task: " message ",
	}))
	require.Equal(t, "1m", resolveEvery(toolInput{Duration: " 1m "}))
	at, err := resolveAt(toolInput{RunAt: "2026"}, time.Now())
	require.NoError(t, err)
	require.Equal(t, "2026", at)
	require.Equal(t, "job-1", resolveJobID(toolInput{JobIDOld: "job-1"}))
	require.True(t, hasScheduleInput(toolInput{CronExpr: "*/1 * * * *"}))
	require.Equal(
		t,
		ScheduleKindCron,
		resolveScheduleKind("", "", "", 0, "*/5 * * * *"),
	)
}

func TestTool_UpdateAndRemoveSuccessPaths(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
	)
	require.NoError(t, err)

	job, err := svc.Add(&Job{
		Name:    "mine",
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
		"action":         "update",
		"job_id":         job.ID,
		"enabled":        false,
		"timeout_sec":    9,
		"channel":        "telegram",
		"target":         "12345",
		"max_runs":       3,
		"overlap_policy": OverlapPolicyReplace,
		"schedule_kind":  "every",
		"every":          "5m",
	})
	require.NoError(t, err)

	out, err := tool.Call(ctx, args)
	require.NoError(t, err)
	updated := out.(*Job)
	require.False(t, updated.Enabled)
	require.Equal(t, 9, updated.TimeoutSec)
	require.Equal(t, 3, updated.Policy.MaxRuns)
	require.Equal(
		t,
		OverlapPolicyReplace,
		updated.Policy.OverlapPolicy,
	)
	require.Equal(t, "5m", updated.Schedule.Every)

	out, err = tool.Call(ctx, []byte(
		`{"action":"remove","job_id":"`+job.ID+`"}`,
	))
	require.NoError(t, err)
	payload := out.(map[string]any)
	require.Equal(t, true, payload["ok"])
	require.Equal(t, job.ID, payload["job_id"])
}

func TestTool_ContextAndDeliveryErrors(t *testing.T) {
	t.Parallel()

	svc, err := NewService(
		t.TempDir(),
		&stubRunner{reply: "ok"},
		outbound.NewRouter(),
	)
	require.NoError(t, err)

	tool := NewTool(svc)

	_, err = tool.Call(context.Background(), []byte(`{"action":"list"}`))
	require.Error(t, err)

	_, err = currentUserID(context.Background())
	require.Error(t, err)

	_, err = resolveDelivery(context.Background(), "", "", false)
	require.ErrorContains(t, err, errDeliveryTargetUnavailable)

	delivery, err := resolveDelivery(
		context.Background(),
		"",
		"",
		true,
	)
	require.NoError(t, err)
	require.Equal(t, outbound.DeliveryTarget{}, delivery)

	_, err = optionalScopeDelivery(
		context.Background(),
		"telegram",
		"",
	)
	require.Error(t, err)

	require.False(t, isScheduledRunMutation(context.Background(), actionList))
	require.Equal(t, 3, firstIntValue(nil, intPointer(3)))
	require.Equal(t, int64(5), firstInt64Value(nil, int64Pointer(5)))
	require.Equal(t, "x", firstString("", " x "))
}

func TestTool_ErrorsAndHelperBranches(t *testing.T) {
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

	_, err = tool.Call(ctx, []byte("{"))
	require.ErrorContains(t, err, "invalid args")

	_, err = tool.Call(ctx, []byte(`{"action":"unknown"}`))
	require.ErrorContains(t, err, "unsupported cron action")

	_, err = tool.Call(
		ctx,
		[]byte(`{
			"action":"add",
			"message":"report",
			"every":"1m",
			"headless":true,
			"target":"12345"
		}`),
	)
	require.ErrorContains(t, err, errHeadlessWithTarget)

	_, err = parseOptionalRFC3339("bad")
	require.ErrorContains(t, err, "invalid ends_at")

	require.True(t, boolValue(boolPointer(true)))
	require.False(t, boolValue(nil))
	require.True(t, hasPolicyInput(toolInput{OverlapOld: "replace"}))
	require.Equal(t, "", firstString("", " "))
}

func intPointer(v int) *int {
	return &v
}

func int64Pointer(v int64) *int64 {
	return &v
}

func boolPointer(v bool) *bool {
	return &v
}
