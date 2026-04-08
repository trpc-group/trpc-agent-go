//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package manager

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	promptiter "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

type fakePromptIterEngine struct {
	describe func(ctx context.Context) (*astructure.Snapshot, error)
	run      func(ctx context.Context, request *promptiterengine.RunRequest, opts ...promptiterengine.Option) (*promptiterengine.RunResult, error)
}

func (f *fakePromptIterEngine) Describe(ctx context.Context) (*astructure.Snapshot, error) {
	if f.describe != nil {
		return f.describe(ctx)
	}
	return &astructure.Snapshot{StructureID: "structure_1", EntryNodeID: "node_1"}, nil
}

func (f *fakePromptIterEngine) Run(
	ctx context.Context,
	request *promptiterengine.RunRequest,
	opts ...promptiterengine.Option,
) (*promptiterengine.RunResult, error) {
	if f.run != nil {
		return f.run(ctx, request, opts...)
	}
	return &promptiterengine.RunResult{}, nil
}

func TestManagerStartAndGetReturnRun(t *testing.T) {
	engineInstance := &fakePromptIterEngine{
		run: func(ctx context.Context, request *promptiterengine.RunRequest, opts ...promptiterengine.Option) (*promptiterengine.RunResult, error) {
			require.NotNil(t, ctx)
			require.NotNil(t, request)
			require.Len(t, opts, 1)
			return &promptiterengine.RunResult{
				Structure:          &astructure.Snapshot{StructureID: "structure_1", EntryNodeID: "node_1"},
				BaselineValidation: newEvaluationResult(0.62),
				AcceptedProfile:    &promptiter.Profile{StructureID: "structure_1"},
				Rounds: []promptiterengine.RoundResult{
					{
						Round:      1,
						Train:      newEvaluationResult(0.61),
						Validation: newEvaluationResult(0.64),
						Acceptance: &promptiterengine.AcceptanceDecision{
							Accepted:   true,
							ScoreDelta: 0.02,
							Reason:     "accepted",
						},
					},
				},
			}, nil
		},
	}
	managerInstance, err := New(engineInstance)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	run, err := managerInstance.Start(context.Background(), &promptiterengine.RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		MaxRounds:            1,
		TargetSurfaceIDs:     []string{"candidate#instruction"},
	})
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.Equal(t, promptiterengine.RunStatusQueued, run.Status)
	assert.NotEmpty(t, run.ID)
	require.Eventually(t, func() bool {
		current, getErr := managerInstance.Get(context.Background(), run.ID)
		require.NoError(t, getErr)
		return current.Status == promptiterengine.RunStatusSucceeded
	}, time.Second, 10*time.Millisecond)
	current, err := managerInstance.Get(context.Background(), run.ID)
	require.NoError(t, err)
	require.NotNil(t, current)
	assert.Equal(t, run.ID, current.ID)
	assert.Equal(t, promptiterengine.RunStatusSucceeded, current.Status)
	require.NotNil(t, current.Structure)
	assert.Equal(t, "structure_1", current.Structure.StructureID)
	require.NotNil(t, current.BaselineValidation)
	assert.InDelta(t, 0.62, current.BaselineValidation.OverallScore, 0.0001)
	require.NotNil(t, current.AcceptedProfile)
	assert.Equal(t, "structure_1", current.AcceptedProfile.StructureID)
	require.Len(t, current.Rounds, 1)
	assert.Equal(t, 1, current.Rounds[0].Round)
	require.NotNil(t, current.Rounds[0].Train)
	assert.InDelta(t, 0.61, current.Rounds[0].Train.OverallScore, 0.0001)
	require.NotNil(t, current.Rounds[0].Validation)
	assert.InDelta(t, 0.64, current.Rounds[0].Validation.OverallScore, 0.0001)
	require.NotNil(t, current.Rounds[0].Acceptance)
	assert.True(t, current.Rounds[0].Acceptance.Accepted)
}

func TestManagerCancelTransitionsRun(t *testing.T) {
	engineInstance := &fakePromptIterEngine{
		run: func(ctx context.Context, request *promptiterengine.RunRequest, opts ...promptiterengine.Option) (*promptiterengine.RunResult, error) {
			require.NotNil(t, request)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	managerInstance, err := New(engineInstance)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	run, err := managerInstance.Start(context.Background(), &promptiterengine.RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		MaxRounds:            1,
	})
	require.NoError(t, err)
	require.NoError(t, managerInstance.Cancel(context.Background(), run.ID))
	require.Eventually(t, func() bool {
		current, getErr := managerInstance.Get(context.Background(), run.ID)
		require.NoError(t, getErr)
		return current.Status == promptiterengine.RunStatusCanceled
	}, time.Second, 10*time.Millisecond)
	current, err := managerInstance.Get(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, promptiterengine.RunStatusCanceled, current.Status)
	assert.NotEmpty(t, current.ErrorMessage)
}

func TestRunObserverBuildsIncrementalRun(t *testing.T) {
	managerInstance, err := New(&fakePromptIterEngine{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	concreteManager, ok := managerInstance.(*manager)
	require.True(t, ok)
	run := &promptiterengine.RunResult{
		ID:     "run-1",
		Status: promptiterengine.RunStatusRunning,
	}
	require.NoError(t, concreteManager.store.Create(context.Background(), run))
	observer := &observer{
		manager: concreteManager,
		run:     run,
	}
	require.NoError(t, observer.append(context.Background(), &promptiterengine.Event{
		Kind:    promptiterengine.EventKindStructureSnapshot,
		Payload: &astructure.Snapshot{StructureID: "structure_1", EntryNodeID: "node_1"},
	}))
	require.NoError(t, observer.append(context.Background(), &promptiterengine.Event{
		Kind:    promptiterengine.EventKindBaselineValidation,
		Payload: newEvaluationResult(0.55),
	}))
	require.NoError(t, observer.append(context.Background(), &promptiterengine.Event{
		Kind:  promptiterengine.EventKindRoundStarted,
		Round: 1,
	}))
	require.NoError(t, observer.append(context.Background(), &promptiterengine.Event{
		Kind:    promptiterengine.EventKindRoundTrainEvaluation,
		Round:   1,
		Payload: newEvaluationResult(0.60),
	}))
	require.NoError(t, observer.append(context.Background(), &promptiterengine.Event{
		Kind:  promptiterengine.EventKindRoundLosses,
		Round: 1,
		Payload: []promptiter.CaseLoss{
			{
				EvalSetID:  "train",
				EvalCaseID: "case_1",
			},
		},
	}))
	require.NoError(t, observer.append(context.Background(), &promptiterengine.Event{
		Kind:  promptiterengine.EventKindRoundBackward,
		Round: 1,
		Payload: &promptiterengine.BackwardResult{
			Cases: []promptiterengine.CaseBackwardResult{},
		},
	}))
	require.NoError(t, observer.append(context.Background(), &promptiterengine.Event{
		Kind:  promptiterengine.EventKindRoundAggregation,
		Round: 1,
		Payload: &promptiterengine.AggregationResult{
			Surfaces: []promptiter.AggregatedSurfaceGradient{},
		},
	}))
	require.NoError(t, observer.append(context.Background(), &promptiterengine.Event{
		Kind:  promptiterengine.EventKindRoundPatchSet,
		Round: 1,
		Payload: &promptiter.PatchSet{
			Patches: []promptiter.SurfacePatch{},
		},
	}))
	require.NoError(t, observer.append(context.Background(), &promptiterengine.Event{
		Kind:  promptiterengine.EventKindRoundOutputProfile,
		Round: 1,
		Payload: &promptiter.Profile{
			StructureID: "structure_1",
		},
	}))
	require.NoError(t, observer.append(context.Background(), &promptiterengine.Event{
		Kind:    promptiterengine.EventKindRoundValidation,
		Round:   1,
		Payload: newEvaluationResult(0.75),
	}))
	require.NoError(t, observer.append(context.Background(), &promptiterengine.Event{
		Kind:  promptiterengine.EventKindRoundCompleted,
		Round: 1,
		Payload: &promptiterengine.RoundCompleted{
			Accepted:         true,
			AcceptanceReason: "accepted",
			ScoreDelta:       0.20,
			ShouldStop:       true,
			StopReason:       "target reached",
		},
	}))
	current, err := concreteManager.Get(context.Background(), run.ID)
	require.NoError(t, err)
	require.NotNil(t, current)
	require.NotNil(t, current.Structure)
	assert.Equal(t, "structure_1", current.Structure.StructureID)
	require.NotNil(t, current.BaselineValidation)
	assert.InDelta(t, 0.55, current.BaselineValidation.OverallScore, 0.0001)
	require.Len(t, current.Rounds, 1)
	assert.Equal(t, 1, current.Rounds[0].Round)
	require.NotNil(t, current.Rounds[0].Train)
	assert.InDelta(t, 0.60, current.Rounds[0].Train.OverallScore, 0.0001)
	require.NotNil(t, current.Rounds[0].Validation)
	assert.InDelta(t, 0.75, current.Rounds[0].Validation.OverallScore, 0.0001)
	require.NotNil(t, current.Rounds[0].Acceptance)
	assert.True(t, current.Rounds[0].Acceptance.Accepted)
	require.NotNil(t, current.Rounds[0].Stop)
	assert.True(t, current.Rounds[0].Stop.ShouldStop)
	require.NotNil(t, current.AcceptedProfile)
	assert.Equal(t, "structure_1", current.AcceptedProfile.StructureID)
}

func TestManagerGetReturnsNotFoundForMissingRun(t *testing.T) {
	managerInstance, err := New(&fakePromptIterEngine{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	_, err = managerInstance.Get(context.Background(), "missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func newEvaluationResult(score float64) *promptiterengine.EvaluationResult {
	return &promptiterengine.EvaluationResult{
		OverallScore: score,
		EvalSets: []promptiterengine.EvalSetResult{
			{
				EvalSetID:    "evalset_1",
				OverallScore: score,
			},
		},
	}
}
