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
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	promptiter "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	promptiterinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/store/inmemory"
)

type recordingStore struct {
	updateAppName string
	updateCtx     context.Context
	updateRun     *promptiterengine.RunResult
}

func (s *recordingStore) Create(ctx context.Context, appName string, run *promptiterengine.RunResult) error {
	_ = ctx
	_ = appName
	_ = run
	return nil
}

func (s *recordingStore) Get(ctx context.Context, appName, runID string) (*promptiterengine.RunResult, error) {
	_ = ctx
	_ = appName
	_ = runID
	return nil, os.ErrNotExist
}

func (s *recordingStore) Update(ctx context.Context, appName string, run *promptiterengine.RunResult) error {
	s.updateAppName = appName
	s.updateCtx = ctx
	s.updateRun = run
	return nil
}

func (s *recordingStore) Close() error {
	return nil
}

type scriptedStore struct {
	createErr       error
	getErr          error
	updateErr       error
	updateErrAtCall int
	closeErr        error
	closeCalls      int
	updateCalls     int
	runs            map[string]*promptiterengine.RunResult
}

func (s *scriptedStore) Create(ctx context.Context, appName string, run *promptiterengine.RunResult) error {
	_ = ctx
	if s.createErr != nil {
		return s.createErr
	}
	if s.runs == nil {
		s.runs = make(map[string]*promptiterengine.RunResult)
	}
	run.AppName = appName
	s.runs[run.ID] = run
	return nil
}

func (s *scriptedStore) Get(ctx context.Context, appName, runID string) (*promptiterengine.RunResult, error) {
	_ = ctx
	_ = appName
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.runs == nil {
		return nil, os.ErrNotExist
	}
	run, ok := s.runs[runID]
	if !ok {
		return nil, os.ErrNotExist
	}
	return run, nil
}

func (s *scriptedStore) Update(ctx context.Context, appName string, run *promptiterengine.RunResult) error {
	_ = ctx
	s.updateCalls++
	if s.updateErr != nil {
		if s.updateErrAtCall == 0 || s.updateCalls == s.updateErrAtCall {
			return s.updateErr
		}
	}
	if s.runs == nil {
		s.runs = make(map[string]*promptiterengine.RunResult)
	}
	run.AppName = appName
	s.runs[run.ID] = run
	return nil
}

func (s *scriptedStore) Close() error {
	s.closeCalls++
	return s.closeErr
}

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

func testEvalSetInputs(evalSetID string) []promptiterengine.EvalSetInput {
	return []promptiterengine.EvalSetInput{
		{
			EvalSetID: evalSetID,
		},
	}
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
	managerInstance, err := New("demo-app", engineInstance)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	run, err := managerInstance.Start(context.Background(), &promptiterengine.RunRequest{
		Train:            testEvalSetInputs("train"),
		Validation:       testEvalSetInputs("validation"),
		MaxRounds:        1,
		TargetSurfaceIDs: []string{"candidate#instruction"},
	})
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.Equal(t, "demo-app", run.AppName)
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
	assert.Equal(t, "demo-app", current.AppName)
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

func TestManagerStartStoresFinalSlimmedRun(t *testing.T) {
	engineInstance := &fakePromptIterEngine{
		run: func(ctx context.Context, request *promptiterengine.RunRequest, opts ...promptiterengine.Option) (*promptiterengine.RunResult, error) {
			_ = ctx
			_ = request
			_ = opts
			return &promptiterengine.RunResult{
				Structure: &astructure.Snapshot{StructureID: "structure_1", EntryNodeID: "node_1"},
				Status:    promptiterengine.RunStatusSucceeded,
			}, nil
		},
	}
	managerInstance, err := New(
		"demo-app",
		engineInstance,
		WithStoredResultSlimming(promptiterengine.RunResultSlimming{OmitStructure: true}),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	run, err := managerInstance.Start(context.Background(), &promptiterengine.RunRequest{
		Train:      testEvalSetInputs("train"),
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		current, getErr := managerInstance.Get(context.Background(), run.ID)
		require.NoError(t, getErr)
		return current.Status == promptiterengine.RunStatusSucceeded
	}, time.Second, 10*time.Millisecond)
	current, err := managerInstance.Get(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Nil(t, current.Structure)
}

func TestSlimRunResultOmitsConfiguredFields(t *testing.T) {
	result := &promptiterengine.RunResult{
		ID:           "run-1",
		Status:       promptiterengine.RunStatusSucceeded,
		CurrentRound: 1,
		Structure:    &astructure.Snapshot{StructureID: "structure_1", EntryNodeID: "node_1"},
		BaselineValidation: &promptiterengine.EvaluationResult{
			OverallScore: 0.5,
			EvalSets: []promptiterengine.EvalSetResult{
				{
					EvalSetID:    "validation",
					OverallScore: 0.5,
					Cases:        []promptiterengine.CaseResult{{EvalSetID: "validation", EvalCaseID: "case_1"}},
				},
			},
		},
		AcceptedProfile: &promptiter.Profile{
			StructureID: "structure_1",
			Overrides: []promptiter.SurfaceOverride{
				{SurfaceID: "node_1#instruction", Value: astructure.SurfaceValue{Text: stringPtr("accepted")}},
			},
		},
		Rounds: []promptiterengine.RoundResult{
			{
				Round:        1,
				InputProfile: &promptiter.Profile{StructureID: "structure_1"},
				Train: &promptiterengine.EvaluationResult{
					OverallScore: 0.4,
					EvalSets: []promptiterengine.EvalSetResult{
						{
							EvalSetID:    "train",
							OverallScore: 0.4,
							Cases:        []promptiterengine.CaseResult{{EvalSetID: "train", EvalCaseID: "case_1"}},
						},
					},
				},
				Losses:      []promptiter.CaseLoss{{EvalSetID: "train", EvalCaseID: "case_1"}},
				Backward:    &promptiterengine.BackwardResult{Cases: []promptiterengine.CaseBackwardResult{{EvalSetID: "train", EvalCaseID: "case_1"}}},
				Aggregation: &promptiterengine.AggregationResult{Surfaces: []promptiter.AggregatedSurfaceGradient{{SurfaceID: "node_1#instruction", NodeID: "node_1"}}},
				Patches: &promptiter.PatchSet{
					Patches: []promptiter.SurfacePatch{
						{SurfaceID: "node_1#instruction", Value: astructure.SurfaceValue{Text: stringPtr("candidate")}},
					},
				},
				OutputProfile: &promptiter.Profile{StructureID: "structure_1"},
				Validation:    &promptiterengine.EvaluationResult{OverallScore: 0.7},
				Acceptance:    &promptiterengine.AcceptanceDecision{Accepted: true},
				Stop:          &promptiterengine.StopDecision{ShouldStop: true},
			},
		},
	}
	assert.Same(t, result, slimRunResult(result, promptiterengine.RunResultSlimming{}))
	assert.Nil(t, slimRunResult(nil, promptiterengine.RunResultSlimming{OmitStructure: true}))
	slimmed := slimRunResult(result, promptiterengine.RunResultSlimming{
		OmitStructure:       true,
		OmitEvaluationCases: true,
		OmitBackward:        true,
		OmitAggregation:     true,
		OmitPatches:         true,
		OmitProfiles:        true,
		OmitLosses:          true,
	})
	require.NotSame(t, result, slimmed)
	assert.Nil(t, slimmed.Structure)
	assert.Nil(t, slimmed.AcceptedProfile)
	require.NotNil(t, slimmed.BaselineValidation)
	require.Len(t, slimmed.BaselineValidation.EvalSets, 1)
	assert.Empty(t, slimmed.BaselineValidation.EvalSets[0].Cases)
	require.Len(t, slimmed.Rounds, 1)
	round := slimmed.Rounds[0]
	assert.Nil(t, round.InputProfile)
	assert.Nil(t, round.OutputProfile)
	assert.Nil(t, round.Backward)
	assert.Nil(t, round.Aggregation)
	assert.Nil(t, round.Patches)
	assert.Empty(t, round.Losses)
	require.NotNil(t, round.Train)
	require.Len(t, round.Train.EvalSets, 1)
	assert.Empty(t, round.Train.EvalSets[0].Cases)
	require.NotNil(t, round.Acceptance)
	require.NotNil(t, round.Stop)
	require.NotNil(t, result.Structure)
	require.Len(t, result.BaselineValidation.EvalSets[0].Cases, 1)
	require.NotNil(t, result.Rounds[0].Backward)
}

func TestSlimStoredRunReturnsOriginalForNilManager(t *testing.T) {
	run := &promptiterengine.RunResult{ID: "run-1"}
	var managerInstance *manager
	assert.Same(t, run, managerInstance.slimStoredRun(run))
}

func TestManagerCancelTransitionsRun(t *testing.T) {
	engineInstance := &fakePromptIterEngine{
		run: func(ctx context.Context, request *promptiterengine.RunRequest, opts ...promptiterengine.Option) (*promptiterengine.RunResult, error) {
			require.NotNil(t, request)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	managerInstance, err := New("demo-app", engineInstance)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	run, err := managerInstance.Start(context.Background(), &promptiterengine.RunRequest{
		Train:      testEvalSetInputs("train"),
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
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
	managerInstance, err := New("demo-app", &fakePromptIterEngine{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	concreteManager, ok := managerInstance.(*manager)
	require.True(t, ok)
	run := &promptiterengine.RunResult{
		AppName: "demo-app",
		ID:      "run-1",
		Status:  promptiterengine.RunStatusRunning,
	}
	require.NoError(t, concreteManager.store.Create(context.Background(), "demo-app", run))
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

func TestRunObserverPassesContextToStoreUpdate(t *testing.T) {
	store := &recordingStore{}
	managerInstance, err := New("demo-app", &fakePromptIterEngine{}, WithStore(store))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	concreteManager, ok := managerInstance.(*manager)
	require.True(t, ok)
	run := &promptiterengine.RunResult{
		AppName: "demo-app",
		ID:      "run-ctx",
		Status:  promptiterengine.RunStatusRunning,
	}
	observer := &observer{
		manager: concreteManager,
		run:     run,
	}
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "store-update")
	err = observer.append(ctx, &promptiterengine.Event{
		Kind:    promptiterengine.EventKindBaselineValidation,
		Payload: newEvaluationResult(0.55),
	})
	require.NoError(t, err)
	require.NotNil(t, store.updateCtx)
	assert.Equal(t, "store-update", store.updateCtx.Value(ctxKey{}))
	assert.Equal(t, "demo-app", store.updateAppName)
	require.NotNil(t, store.updateRun)
	assert.Equal(t, "demo-app", store.updateRun.AppName)
	assert.Equal(t, run.ID, store.updateRun.ID)
	assert.Equal(t, run.Status, store.updateRun.Status)
	require.NotNil(t, store.updateRun.BaselineValidation)
	assert.InDelta(t, 0.55, store.updateRun.BaselineValidation.OverallScore, 0.0001)
}

func TestRunObserverStoresSlimmedCopy(t *testing.T) {
	store := &recordingStore{}
	managerInstance, err := New("demo-app", &fakePromptIterEngine{},
		WithStore(store),
		WithStoredResultSlimming(promptiterengine.RunResultSlimming{
			OmitStructure:       true,
			OmitEvaluationCases: true,
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	concreteManager, ok := managerInstance.(*manager)
	require.True(t, ok)
	run := &promptiterengine.RunResult{
		AppName: "demo-app",
		ID:      "run-slim",
		Status:  promptiterengine.RunStatusRunning,
	}
	observer := &observer{
		manager: concreteManager,
		run:     run,
	}
	require.NoError(t, observer.append(context.Background(), &promptiterengine.Event{
		Kind:    promptiterengine.EventKindStructureSnapshot,
		Payload: &astructure.Snapshot{StructureID: "structure_1", EntryNodeID: "node_1"},
	}))
	require.NoError(t, observer.append(context.Background(), &promptiterengine.Event{
		Kind: promptiterengine.EventKindBaselineValidation,
		Payload: &promptiterengine.EvaluationResult{
			OverallScore: 0.55,
			EvalSets: []promptiterengine.EvalSetResult{
				{
					EvalSetID:    "validation",
					OverallScore: 0.55,
					Cases: []promptiterengine.CaseResult{
						{EvalSetID: "validation", EvalCaseID: "case_1"},
					},
				},
			},
		},
	}))

	require.NotNil(t, observer.run.Structure)
	require.NotNil(t, observer.run.BaselineValidation)
	require.Len(t, observer.run.BaselineValidation.EvalSets[0].Cases, 1)
	require.NotNil(t, store.updateRun)
	assert.Nil(t, store.updateRun.Structure)
	require.NotNil(t, store.updateRun.BaselineValidation)
	require.Len(t, store.updateRun.BaselineValidation.EvalSets, 1)
	assert.Empty(t, store.updateRun.BaselineValidation.EvalSets[0].Cases)
}

func TestRunObserverRejectsInvalidEvents(t *testing.T) {
	managerInstance, err := New("demo-app", &fakePromptIterEngine{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	concreteManager := managerInstance.(*manager)
	observer := &observer{
		manager: concreteManager,
		run: &promptiterengine.RunResult{
			AppName: "demo-app",
			ID:      "run-1",
			Status:  promptiterengine.RunStatusRunning,
		},
	}
	require.NoError(t, concreteManager.store.Create(context.Background(), "demo-app", observer.run))
	assert.EqualError(t, observer.append(context.Background(), nil), "promptiter event is nil")
	testCases := []struct {
		name       string
		event      *promptiterengine.Event
		errContain string
	}{
		{
			name: "invalid structure payload",
			event: &promptiterengine.Event{
				Kind:    promptiterengine.EventKindStructureSnapshot,
				Payload: "invalid",
			},
			errContain: `event "structure_snapshot" payload is invalid`,
		},
		{
			name: "invalid baseline payload",
			event: &promptiterengine.Event{
				Kind:    promptiterengine.EventKindBaselineValidation,
				Payload: "invalid",
			},
			errContain: `event "baseline_validation" payload is invalid`,
		},
		{
			name: "run-level event uses non-zero round",
			event: &promptiterengine.Event{
				Kind:  promptiterengine.EventKindBaselineValidation,
				Round: 1,
			},
			errContain: `event "baseline_validation" must use round 0, got 1`,
		},
		{
			name: "invalid train payload",
			event: &promptiterengine.Event{
				Kind:    promptiterengine.EventKindRoundTrainEvaluation,
				Round:   1,
				Payload: "invalid",
			},
			errContain: `event "round_train_evaluation" payload is invalid`,
		},
		{
			name: "round event uses non-positive round",
			event: &promptiterengine.Event{
				Kind:  promptiterengine.EventKindRoundStarted,
				Round: 0,
			},
			errContain: `event "round_started" must use round >= 1, got 0`,
		},
		{
			name: "invalid losses payload",
			event: &promptiterengine.Event{
				Kind:    promptiterengine.EventKindRoundLosses,
				Round:   1,
				Payload: "invalid",
			},
			errContain: `event "round_losses" payload is invalid`,
		},
		{
			name: "invalid backward payload",
			event: &promptiterengine.Event{
				Kind:    promptiterengine.EventKindRoundBackward,
				Round:   1,
				Payload: "invalid",
			},
			errContain: `event "round_backward" payload is invalid`,
		},
		{
			name: "invalid aggregation payload",
			event: &promptiterengine.Event{
				Kind:    promptiterengine.EventKindRoundAggregation,
				Round:   1,
				Payload: "invalid",
			},
			errContain: `event "round_aggregation" payload is invalid`,
		},
		{
			name: "invalid patch payload",
			event: &promptiterengine.Event{
				Kind:    promptiterengine.EventKindRoundPatchSet,
				Round:   1,
				Payload: "invalid",
			},
			errContain: `event "round_patch_set" payload is invalid`,
		},
		{
			name: "invalid profile payload",
			event: &promptiterengine.Event{
				Kind:    promptiterengine.EventKindRoundOutputProfile,
				Round:   1,
				Payload: "invalid",
			},
			errContain: `event "round_output_profile" payload is invalid`,
		},
		{
			name: "invalid validation payload",
			event: &promptiterengine.Event{
				Kind:    promptiterengine.EventKindRoundValidation,
				Round:   1,
				Payload: "invalid",
			},
			errContain: `event "round_validation" payload is invalid`,
		},
		{
			name: "invalid completed payload",
			event: &promptiterengine.Event{
				Kind:    promptiterengine.EventKindRoundCompleted,
				Round:   1,
				Payload: "invalid",
			},
			errContain: `event "round_completed" payload is invalid`,
		},
		{
			name: "unsupported kind",
			event: &promptiterengine.Event{
				Kind: "unknown",
			},
			errContain: `promptiter event kind "unknown" is unsupported`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := observer.append(context.Background(), tc.event)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.errContain)
		})
	}
}

func TestManagerGetReturnsNotFoundForMissingRun(t *testing.T) {
	managerInstance, err := New("demo-app", &fakePromptIterEngine{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	_, err = managerInstance.Get(context.Background(), "missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestManagerGetIsolatesSharedStoreByAppName(t *testing.T) {
	ctx := context.Background()
	sharedStore := promptiterinmemory.New()
	managerA, err := New("app-a", &fakePromptIterEngine{}, WithStore(sharedStore))
	require.NoError(t, err)
	managerB, err := New("app-b", &fakePromptIterEngine{}, WithStore(sharedStore))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerA.Close())
		require.NoError(t, managerB.Close())
	})
	require.NoError(t, sharedStore.Create(ctx, "app-a", &promptiterengine.RunResult{
		ID:     "run-1",
		Status: promptiterengine.RunStatusQueued,
	}))
	require.NoError(t, sharedStore.Create(ctx, "app-b", &promptiterengine.RunResult{
		ID:     "run-1",
		Status: promptiterengine.RunStatusSucceeded,
	}))
	runA, err := managerA.Get(ctx, "run-1")
	require.NoError(t, err)
	runB, err := managerB.Get(ctx, "run-1")
	require.NoError(t, err)
	assert.Equal(t, "app-a", runA.AppName)
	assert.Equal(t, promptiterengine.RunStatusQueued, runA.Status)
	assert.Equal(t, "app-b", runB.AppName)
	assert.Equal(t, promptiterengine.RunStatusSucceeded, runB.Status)
}

func TestManagerStartRejectsClosedManager(t *testing.T) {
	managerInstance, err := New("demo-app", &fakePromptIterEngine{})
	require.NoError(t, err)
	require.NoError(t, managerInstance.Close())
	run, err := managerInstance.Start(context.Background(), &promptiterengine.RunRequest{
		Train:      testEvalSetInputs("train"),
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
	})
	assert.Nil(t, run)
	assert.EqualError(t, err, "promptiter manager is closed")
}

func TestNewTrimsAppName(t *testing.T) {
	managerInstance, err := New(" demo-app ", &fakePromptIterEngine{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	concreteManager := managerInstance.(*manager)
	assert.Equal(t, "demo-app", concreteManager.appName)
}

func TestNewRejectsNilEngine(t *testing.T) {
	managerInstance, err := New("demo-app", nil)
	assert.Nil(t, managerInstance)
	assert.EqualError(t, err, "promptiter manager: engine must not be nil")
	managerInstance, err = New("", &fakePromptIterEngine{})
	assert.Nil(t, managerInstance)
	assert.EqualError(t, err, "promptiter manager: app name must not be empty")
}

func TestManagerStartReturnsCreateError(t *testing.T) {
	store := &scriptedStore{createErr: errors.New("create failed")}
	managerInstance, err := New("demo-app", &fakePromptIterEngine{}, WithStore(store))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	run, err := managerInstance.Start(context.Background(), &promptiterengine.RunRequest{
		Train:      testEvalSetInputs("train"),
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
	})
	assert.Nil(t, run)
	assert.ErrorContains(t, err, "create run")
	assert.ErrorContains(t, err, "create failed")
}

func TestManagerCancelRejectsMissingRun(t *testing.T) {
	managerInstance, err := New("demo-app", &fakePromptIterEngine{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	err = managerInstance.Cancel(context.Background(), "missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestManagerCancelReturnsStoreErrors(t *testing.T) {
	t.Run("get error", func(t *testing.T) {
		store := &scriptedStore{
			getErr: errors.New("load failed"),
			runs: map[string]*promptiterengine.RunResult{
				"run-1": {
					AppName: "demo-app",
					ID:      "run-1",
					Status:  promptiterengine.RunStatusRunning,
				},
			},
		}
		managerInstance, err := New("demo-app", &fakePromptIterEngine{}, WithStore(store))
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, managerInstance.Close())
		})
		concreteManager := managerInstance.(*manager)
		concreteManager.cancelFuncs["run-1"] = func() {}
		err = managerInstance.Cancel(context.Background(), "run-1")
		assert.EqualError(t, err, "load failed")
	})
	t.Run("update error", func(t *testing.T) {
		store := &scriptedStore{
			updateErr: errors.New("update failed"),
			runs: map[string]*promptiterengine.RunResult{
				"run-1": {
					AppName: "demo-app",
					ID:      "run-1",
					Status:  promptiterengine.RunStatusRunning,
				},
			},
		}
		managerInstance, err := New("demo-app", &fakePromptIterEngine{}, WithStore(store))
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, managerInstance.Close())
		})
		concreteManager := managerInstance.(*manager)
		concreteManager.cancelFuncs["run-1"] = func() {}
		err = managerInstance.Cancel(context.Background(), "run-1")
		assert.EqualError(t, err, "update failed")
	})
}

func TestManagerCloseIsIdempotent(t *testing.T) {
	store := &scriptedStore{}
	managerInstance, err := New("demo-app", &fakePromptIterEngine{}, WithStore(store))
	require.NoError(t, err)
	require.NoError(t, managerInstance.Close())
	require.NoError(t, managerInstance.Close())
	assert.Equal(t, 1, store.closeCalls)
}

func TestManagerRunHandlesErrorBranches(t *testing.T) {
	t.Run("get error clears cancel", func(t *testing.T) {
		store := &scriptedStore{getErr: errors.New("load failed")}
		managerInstance, err := New("demo-app", &fakePromptIterEngine{}, WithStore(store))
		require.NoError(t, err)
		concreteManager := managerInstance.(*manager)
		concreteManager.cancelFuncs["run-1"] = func() {}
		concreteManager.run(context.Background(), "run-1", &promptiterengine.RunRequest{})
		_, ok := concreteManager.cancelFuncs["run-1"]
		assert.False(t, ok)
	})
	t.Run("initial update error clears cancel", func(t *testing.T) {
		store := &scriptedStore{
			updateErr:       errors.New("update failed"),
			updateErrAtCall: 1,
			runs: map[string]*promptiterengine.RunResult{
				"run-1": {
					AppName: "demo-app",
					ID:      "run-1",
					Status:  promptiterengine.RunStatusQueued,
				},
			},
		}
		managerInstance, err := New("demo-app", &fakePromptIterEngine{}, WithStore(store))
		require.NoError(t, err)
		concreteManager := managerInstance.(*manager)
		concreteManager.cancelFuncs["run-1"] = func() {}
		concreteManager.run(context.Background(), "run-1", &promptiterengine.RunRequest{})
		_, ok := concreteManager.cancelFuncs["run-1"]
		assert.False(t, ok)
	})
	t.Run("engine canceled updates status", func(t *testing.T) {
		store := &scriptedStore{
			runs: map[string]*promptiterengine.RunResult{
				"run-1": {
					AppName: "demo-app",
					ID:      "run-1",
					Status:  promptiterengine.RunStatusRunning,
				},
			},
		}
		engineInstance := &fakePromptIterEngine{
			run: func(ctx context.Context, request *promptiterengine.RunRequest, opts ...promptiterengine.Option) (*promptiterengine.RunResult, error) {
				_ = request
				_ = opts
				return nil, context.Canceled
			},
		}
		managerInstance, err := New("demo-app", engineInstance, WithStore(store))
		require.NoError(t, err)
		concreteManager := managerInstance.(*manager)
		concreteManager.cancelFuncs["run-1"] = func() {}
		concreteManager.run(context.Background(), "run-1", &promptiterengine.RunRequest{})
		run := store.runs["run-1"]
		assert.Equal(t, promptiterengine.RunStatusCanceled, run.Status)
		assert.Equal(t, "run canceled", run.ErrorMessage)
	})
	t.Run("engine failure updates status", func(t *testing.T) {
		store := &scriptedStore{
			runs: map[string]*promptiterengine.RunResult{
				"run-1": {
					AppName: "demo-app",
					ID:      "run-1",
					Status:  promptiterengine.RunStatusRunning,
				},
			},
		}
		engineInstance := &fakePromptIterEngine{
			run: func(ctx context.Context, request *promptiterengine.RunRequest, opts ...promptiterengine.Option) (*promptiterengine.RunResult, error) {
				_ = ctx
				_ = request
				_ = opts
				return nil, errors.New("engine failed")
			},
		}
		managerInstance, err := New("demo-app", engineInstance, WithStore(store))
		require.NoError(t, err)
		concreteManager := managerInstance.(*manager)
		concreteManager.cancelFuncs["run-1"] = func() {}
		concreteManager.run(context.Background(), "run-1", &promptiterengine.RunRequest{})
		run := store.runs["run-1"]
		assert.Equal(t, promptiterengine.RunStatusFailed, run.Status)
		assert.Equal(t, "engine failed", run.ErrorMessage)
	})
	t.Run("nil engine result updates status", func(t *testing.T) {
		store := &scriptedStore{
			runs: map[string]*promptiterengine.RunResult{
				"run-1": {
					AppName: "demo-app",
					ID:      "run-1",
					Status:  promptiterengine.RunStatusRunning,
				},
			},
		}
		engineInstance := &fakePromptIterEngine{
			run: func(ctx context.Context, request *promptiterengine.RunRequest, opts ...promptiterengine.Option) (*promptiterengine.RunResult, error) {
				_ = ctx
				_ = request
				_ = opts
				return nil, nil
			},
		}
		managerInstance, err := New("demo-app", engineInstance, WithStore(store))
		require.NoError(t, err)
		concreteManager := managerInstance.(*manager)
		concreteManager.cancelFuncs["run-1"] = func() {}
		concreteManager.run(context.Background(), "run-1", &promptiterengine.RunRequest{})
		run := store.runs["run-1"]
		assert.Equal(t, promptiterengine.RunStatusFailed, run.Status)
		assert.Equal(t, "engine returned nil run", run.ErrorMessage)
	})
	t.Run("final update error stores failure", func(t *testing.T) {
		store := &scriptedStore{
			updateErr:       errors.New("persist failed"),
			updateErrAtCall: 2,
			runs: map[string]*promptiterengine.RunResult{
				"run-1": {
					AppName: "demo-app",
					ID:      "run-1",
					Status:  promptiterengine.RunStatusRunning,
				},
			},
		}
		engineInstance := &fakePromptIterEngine{
			run: func(ctx context.Context, request *promptiterengine.RunRequest, opts ...promptiterengine.Option) (*promptiterengine.RunResult, error) {
				_ = ctx
				_ = request
				_ = opts
				return &promptiterengine.RunResult{}, nil
			},
		}
		managerInstance, err := New("demo-app", engineInstance, WithStore(store))
		require.NoError(t, err)
		concreteManager := managerInstance.(*manager)
		concreteManager.cancelFuncs["run-1"] = func() {}
		concreteManager.run(context.Background(), "run-1", &promptiterengine.RunRequest{})
		run := store.runs["run-1"]
		assert.Equal(t, promptiterengine.RunStatusFailed, run.Status)
		assert.Equal(t, "persist failed", run.ErrorMessage)
	})
}

func TestValidateRunRequest(t *testing.T) {
	assert.EqualError(t, validateRunRequest(nil), "run request is nil")
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{}), "train evaluation sets are empty")
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train: testEvalSetInputs("train"),
	}), "validation evaluation sets are empty")
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{
			{
				EvalSetID: "",
			},
		},
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
	}), "train evaluation set id is empty")
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train: testEvalSetInputs("train"),
		Validation: []promptiterengine.EvalSetInput{
			{
				EvalSetID:   "validation",
				EvalCaseIDs: []string{""},
			},
		},
		MaxRounds: 1,
	}), `validation eval case id for eval set "validation" is empty`)
	assert.NoError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{
			{
				EvalSetID: "train",
			},
			{
				EvalSetID: "train",
			},
		},
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
	}))
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{
			{
				EvalSetID: "train",
				LossHints: []promptiterengine.LossHint{
					{
						EvalCaseID: " ",
						MetricName: "quality",
						Reason:     "business reason",
					},
				},
			},
		},
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
	}), `train loss hint eval case id for eval set "train" is empty`)
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{
			{
				EvalSetID: "train",
				LossHints: []promptiterengine.LossHint{
					{
						EvalCaseID: "case_1",
						MetricName: " ",
						Reason:     "business reason",
					},
				},
			},
		},
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
	}), `train loss hint metric name for eval set "train" case "case_1" is empty`)
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{
			{
				EvalSetID: "train",
				LossHints: []promptiterengine.LossHint{
					{
						EvalCaseID: "case_1",
						MetricName: "quality",
						Reason:     " ",
					},
				},
			},
		},
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
	}), `train loss hint reason for eval set "train" case "case_1" metric "quality" is empty`)
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{
			{
				EvalSetID:   "train",
				EvalCaseIDs: []string{"case_1"},
				LossHints: []promptiterengine.LossHint{
					{
						EvalCaseID: "case_2",
						MetricName: "quality",
						Reason:     "business reason",
					},
				},
			},
		},
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
	}), `train loss hint eval case "case_2" is not selected for eval set "train"`)
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{
			{
				EvalSetID: "train",
				LossHints: []promptiterengine.LossHint{
					{
						EvalCaseID: "case_1",
						MetricName: "quality",
						Severity:   promptiter.LossSeverity("P4"),
						Reason:     "business reason",
					},
				},
			},
		},
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
	}), `train loss hint severity "P4" for eval set "train" case "case_1" metric "quality" is invalid`)
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train:      testEvalSetInputs("train"),
		Validation: testEvalSetInputs("validation"),
	}), "max rounds must be greater than 0")
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train:            testEvalSetInputs("train"),
		Validation:       testEvalSetInputs("validation"),
		MaxRounds:        1,
		TargetSurfaceIDs: []string{},
	}), "target surface ids must not be empty")
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train:      testEvalSetInputs("train"),
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
		BackwardOptions: promptiterengine.BackwardOptions{
			CaseParallelism: -1,
		},
	}), "backward case parallelism must be non-negative")
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train:      testEvalSetInputs("train"),
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
		AggregationOptions: promptiterengine.AggregationOptions{
			SurfaceParallelism: -1,
		},
	}), "aggregation surface parallelism must be non-negative")
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train:      testEvalSetInputs("train"),
		Validation: testEvalSetInputs("validation"),
		MaxRounds:  1,
		OptimizerOptions: promptiterengine.OptimizerOptions{
			SurfaceParallelism: -1,
		},
	}), "optimizer surface parallelism must be non-negative")
	assert.NoError(t, validateRunRequest(&promptiterengine.RunRequest{
		Train:            testEvalSetInputs("train"),
		Validation:       testEvalSetInputs("validation"),
		MaxRounds:        1,
		TargetSurfaceIDs: []string{"candidate#instruction"},
		BackwardOptions: promptiterengine.BackwardOptions{
			CaseParallelismEnabled: true,
			CaseParallelism:        1,
		},
		AggregationOptions: promptiterengine.AggregationOptions{
			SurfaceParallelismEnabled: true,
			SurfaceParallelism:        1,
		},
		OptimizerOptions: promptiterengine.OptimizerOptions{
			SurfaceParallelismEnabled: true,
			SurfaceParallelism:        1,
		},
	}))
}

func TestCloneRunRequestDeepCopiesFields(t *testing.T) {
	targetScore := 0.9
	request := &promptiterengine.RunRequest{
		Train: testEvalSetInputs("train"),
		Validation: []promptiterengine.EvalSetInput{
			{
				EvalSetID:   "validation",
				EvalCaseIDs: []string{"case_1"},
				LossHints: []promptiterengine.LossHint{
					{
						EvalCaseID: "case_1",
						MetricName: "quality",
						Severity:   promptiter.LossSeverityP1,
						Reason:     "business reason",
					},
				},
			},
		},
		InitialProfile: &promptiter.Profile{
			StructureID: "structure_1",
			Overrides: []promptiter.SurfaceOverride{
				{
					SurfaceID: "candidate#instruction",
					Value: astructure.SurfaceValue{
						Text: stringPtr("prompt"),
					},
				},
			},
		},
		TargetSurfaceIDs: []string{"candidate#instruction"},
		BackwardOptions: promptiterengine.BackwardOptions{
			CaseParallelismEnabled: true,
			CaseParallelism:        4,
		},
		AggregationOptions: promptiterengine.AggregationOptions{
			SurfaceParallelismEnabled: true,
			SurfaceParallelism:        3,
		},
		OptimizerOptions: promptiterengine.OptimizerOptions{
			SurfaceParallelismEnabled: true,
			SurfaceParallelism:        2,
		},
		StopPolicy: promptiterengine.StopPolicy{
			TargetScore: &targetScore,
		},
	}
	cloned := cloneRunRequest(request)
	require.NotNil(t, cloned)
	cloned.Train[0].EvalSetID = "mutated"
	cloned.Validation[0].EvalCaseIDs[0] = "mutated"
	cloned.Validation[0].LossHints[0].Reason = "mutated"
	cloned.TargetSurfaceIDs[0] = "mutated"
	*cloned.InitialProfile.Overrides[0].Value.Text = "mutated"
	*cloned.StopPolicy.TargetScore = 1.0
	assert.Equal(t, "train", request.Train[0].EvalSetID)
	assert.Equal(t, []string{"case_1"}, request.Validation[0].EvalCaseIDs)
	assert.Equal(t, "business reason", request.Validation[0].LossHints[0].Reason)
	assert.Equal(t, "candidate#instruction", request.TargetSurfaceIDs[0])
	assert.Equal(t, "prompt", *request.InitialProfile.Overrides[0].Value.Text)
	assert.Equal(t, 0.9, *request.StopPolicy.TargetScore)
	assert.Equal(t, promptiterengine.BackwardOptions{CaseParallelismEnabled: true, CaseParallelism: 4}, cloned.BackwardOptions)
	assert.Equal(t, promptiterengine.AggregationOptions{SurfaceParallelismEnabled: true, SurfaceParallelism: 3}, cloned.AggregationOptions)
	assert.Equal(t, promptiterengine.OptimizerOptions{SurfaceParallelismEnabled: true, SurfaceParallelism: 2}, cloned.OptimizerOptions)
}

func TestCloneRunRequestNil(t *testing.T) {
	assert.Nil(t, cloneRunRequest(nil))
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

func stringPtr(value string) *string {
	return &value
}
