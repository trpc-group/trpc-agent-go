//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tracecapture_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/internal/tracecapture"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestInvocationStepOwnsBorrowsAndReleases(t *testing.T) {
	capture := tracecapture.New("root", "inv", "session", time.Now())
	binding := tracecapture.NewStepBinding()
	ctx := tracecapture.AttachInvocationRuntime(context.Background(), binding, capture)

	starts := 0
	start := func() string {
		starts++
		return capture.StartStep(tracecapture.StartStepInput{
			InvocationID: "inv",
			AgentName:    "root",
			NodeID:       "root/node",
		})
	}
	owned := tracecapture.EnsureInvocationStep(ctx, start)
	require.True(t, owned.Owns)
	require.NotEmpty(t, owned.StepID)
	require.Equal(t, 1, starts)

	borrowed := tracecapture.EnsureInvocationStep(ctx, func() string {
		t.Fatal("an exact binding must be borrowed")
		return ""
	})
	require.False(t, borrowed.Owns)
	require.Equal(t, owned.StepID, borrowed.StepID)

	tracecapture.ReleaseInvocationStep(borrowed)
	stillBorrowed := tracecapture.EnsureInvocationStep(ctx, func() string {
		t.Fatal("releasing a borrowed lease must not clear the owner binding")
		return ""
	})
	require.False(t, stillBorrowed.Owns)
	require.Equal(t, owned.StepID, stillBorrowed.StepID)
	tracecapture.ReleaseInvocationStep(owned)

	replacement := tracecapture.EnsureInvocationStep(ctx, start)
	require.True(t, replacement.Owns)
	require.NotEqual(t, owned.StepID, replacement.StepID)
	require.Equal(t, 2, starts)
	tracecapture.ReleaseInvocationStep(owned)
	replacementBorrow := tracecapture.EnsureInvocationStep(ctx, func() string {
		t.Fatal("a stale release must not clear the replacement binding")
		return ""
	})
	require.False(t, replacementBorrow.Owns)
	require.Equal(t, replacement.StepID, replacementBorrow.StepID)
}

func TestInvocationStepUpdatesCurrentCaptureStep(t *testing.T) {
	startedAt := time.Now()
	capture := tracecapture.New("root", "inv", "session", startedAt)
	binding := tracecapture.NewStepBinding()
	ctx := tracecapture.AttachInvocationRuntime(context.Background(), binding, capture)
	stepID := capture.StartStep(tracecapture.StartStepInput{
		InvocationID: "inv",
		AgentName:    "root",
		NodeID:       "root/node",
		Input:        &atrace.Snapshot{Text: "initial"},
	})
	tracecapture.BindInvocationStep(ctx, stepID)

	tracecapture.SetInvocationStepInput(ctx, &atrace.Snapshot{Text: "first request"})
	tracecapture.SetInvocationStepInput(ctx, &atrace.Snapshot{Text: "last request"})
	tracecapture.MergeInvocationStepAppliedSurfaceIDs(ctx, []string{"surface-b", "surface-a", "surface-b"})
	tracecapture.MergeInvocationStepAppliedSurfaceIDs(ctx, []string{"surface-a", "surface-c"})
	tracecapture.AddInvocationStepUsage(ctx, &model.Usage{
		PromptTokens:     2,
		CompletionTokens: 3,
		TotalTokens:      5,
		TimingInfo:       &model.TimingInfo{},
	})
	tracecapture.AddInvocationStepUsage(ctx, &model.Usage{
		PromptTokens:     7,
		CompletionTokens: 11,
		TotalTokens:      18,
	})
	capture.FinishStep(stepID, &atrace.Snapshot{Text: "output"}, "", time.Now())

	result := capture.Build(atrace.TraceStatusCompleted, time.Now())
	require.Len(t, result.Steps, 1)
	step := result.Steps[0]
	require.Equal(t, "last request", step.Input.Text)
	require.Equal(t, []string{"surface-b", "surface-a", "surface-c"}, step.AppliedSurfaceIDs)
	require.Equal(t, &model.Usage{
		PromptTokens:     9,
		CompletionTokens: 14,
		TotalTokens:      23,
	}, step.Usage)
}

func TestInvocationRuntimeMasksParent(t *testing.T) {
	plainCtx := context.WithValue(context.Background(), struct{}{}, "plain")
	require.Same(t, plainCtx, tracecapture.AttachInvocationRuntime(plainCtx, nil, nil))

	parentCapture := tracecapture.New("parent", "parent-inv", "session", time.Now())
	parentBinding := tracecapture.NewStepBinding()
	parentCtx := tracecapture.AttachInvocationRuntime(
		context.Background(),
		parentBinding,
		parentCapture,
	)
	stepID := parentCapture.StartStep(tracecapture.StartStepInput{
		InvocationID: "parent-inv",
		AgentName:    "parent",
		NodeID:       "parent/node",
		Input:        &atrace.Snapshot{Text: "parent input"},
	})
	tracecapture.BindInvocationStep(parentCtx, stepID)

	maskedCtx := tracecapture.AttachInvocationRuntime(parentCtx, nil, nil)
	tracecapture.SetInvocationStepInput(maskedCtx, &atrace.Snapshot{Text: "must not apply"})

	result := parentCapture.Build(atrace.TraceStatusCompleted, time.Now())
	require.Len(t, result.Steps, 1)
	require.Equal(t, "parent input", result.Steps[0].Input.Text)
}
