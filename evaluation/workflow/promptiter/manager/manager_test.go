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
)

type recordingStore struct {
	updateCtx context.Context
	updateRun *promptiterengine.RunResult
}

func (s *recordingStore) Create(ctx context.Context, run *promptiterengine.RunResult) error {
	_ = ctx
	_ = run
	return nil
}

func (s *recordingStore) Get(ctx context.Context, runID string) (*promptiterengine.RunResult, error) {
	_ = ctx
	_ = runID
	return nil, os.ErrNotExist
}

func (s *recordingStore) Update(ctx context.Context, run *promptiterengine.RunResult) error {
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

func (s *scriptedStore) Create(ctx context.Context, run *promptiterengine.RunResult) error {
	_ = ctx
	if s.createErr != nil {
		return s.createErr
	}
	if s.runs == nil {
		s.runs = make(map[string]*promptiterengine.RunResult)
	}
	s.runs[run.ID] = run
	return nil
}

func (s *scriptedStore) Get(ctx context.Context, runID string) (*promptiterengine.RunResult, error) {
	_ = ctx
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

func (s *scriptedStore) Update(ctx context.Context, run *promptiterengine.RunResult) error {
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

func TestRunObserverPassesContextToStoreUpdate(t *testing.T) {
	store := &recordingStore{}
	managerInstance, err := New(&fakePromptIterEngine{}, WithStore(store))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	concreteManager, ok := managerInstance.(*manager)
	require.True(t, ok)
	run := &promptiterengine.RunResult{
		ID:     "run-ctx",
		Status: promptiterengine.RunStatusRunning,
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
	require.NotNil(t, store.updateRun)
	assert.Equal(t, run.ID, store.updateRun.ID)
	assert.Equal(t, run.Status, store.updateRun.Status)
	require.NotNil(t, store.updateRun.BaselineValidation)
	assert.InDelta(t, 0.55, store.updateRun.BaselineValidation.OverallScore, 0.0001)
}

func TestRunObserverRejectsInvalidEvents(t *testing.T) {
	managerInstance, err := New(&fakePromptIterEngine{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	concreteManager := managerInstance.(*manager)
	observer := &observer{
		manager: concreteManager,
		run: &promptiterengine.RunResult{
			ID:     "run-1",
			Status: promptiterengine.RunStatusRunning,
		},
	}
	require.NoError(t, concreteManager.store.Create(context.Background(), observer.run))
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
	managerInstance, err := New(&fakePromptIterEngine{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	_, err = managerInstance.Get(context.Background(), "missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestManagerStartRejectsClosedManager(t *testing.T) {
	managerInstance, err := New(&fakePromptIterEngine{})
	require.NoError(t, err)
	require.NoError(t, managerInstance.Close())
	run, err := managerInstance.Start(context.Background(), &promptiterengine.RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		MaxRounds:            1,
	})
	assert.Nil(t, run)
	assert.EqualError(t, err, "promptiter manager is closed")
}

func TestNewRejectsNilEngine(t *testing.T) {
	managerInstance, err := New(nil)
	assert.Nil(t, managerInstance)
	assert.EqualError(t, err, "promptiter manager: engine must not be nil")
}

func TestManagerStartReturnsCreateError(t *testing.T) {
	store := &scriptedStore{createErr: errors.New("create failed")}
	managerInstance, err := New(&fakePromptIterEngine{}, WithStore(store))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, managerInstance.Close())
	})
	run, err := managerInstance.Start(context.Background(), &promptiterengine.RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		MaxRounds:            1,
	})
	assert.Nil(t, run)
	assert.ErrorContains(t, err, "create run")
	assert.ErrorContains(t, err, "create failed")
}

func TestManagerCancelRejectsMissingRun(t *testing.T) {
	managerInstance, err := New(&fakePromptIterEngine{})
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
					ID:     "run-1",
					Status: promptiterengine.RunStatusRunning,
				},
			},
		}
		managerInstance, err := New(&fakePromptIterEngine{}, WithStore(store))
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
					ID:     "run-1",
					Status: promptiterengine.RunStatusRunning,
				},
			},
		}
		managerInstance, err := New(&fakePromptIterEngine{}, WithStore(store))
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
	managerInstance, err := New(&fakePromptIterEngine{}, WithStore(store))
	require.NoError(t, err)
	require.NoError(t, managerInstance.Close())
	require.NoError(t, managerInstance.Close())
	assert.Equal(t, 1, store.closeCalls)
}

func TestManagerRunHandlesErrorBranches(t *testing.T) {
	t.Run("get error clears cancel", func(t *testing.T) {
		store := &scriptedStore{getErr: errors.New("load failed")}
		managerInstance, err := New(&fakePromptIterEngine{}, WithStore(store))
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
					ID:     "run-1",
					Status: promptiterengine.RunStatusQueued,
				},
			},
		}
		managerInstance, err := New(&fakePromptIterEngine{}, WithStore(store))
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
					ID:     "run-1",
					Status: promptiterengine.RunStatusRunning,
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
		managerInstance, err := New(engineInstance, WithStore(store))
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
					ID:     "run-1",
					Status: promptiterengine.RunStatusRunning,
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
		managerInstance, err := New(engineInstance, WithStore(store))
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
					ID:     "run-1",
					Status: promptiterengine.RunStatusRunning,
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
		managerInstance, err := New(engineInstance, WithStore(store))
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
					ID:     "run-1",
					Status: promptiterengine.RunStatusRunning,
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
		managerInstance, err := New(engineInstance, WithStore(store))
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
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{}), "train evaluation set ids are empty")
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		TrainEvalSetIDs: []string{"train"},
	}), "validation evaluation set ids are empty")
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
	}), "max rounds must be greater than 0")
	assert.EqualError(t, validateRunRequest(&promptiterengine.RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		MaxRounds:            1,
		TargetSurfaceIDs:     []string{},
	}), "target surface ids must not be empty")
	assert.NoError(t, validateRunRequest(&promptiterengine.RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
		MaxRounds:            1,
		TargetSurfaceIDs:     []string{"candidate#instruction"},
	}))
}

func TestCloneRunRequestDeepCopiesFields(t *testing.T) {
	targetScore := 0.9
	request := &promptiterengine.RunRequest{
		TrainEvalSetIDs:      []string{"train"},
		ValidationEvalSetIDs: []string{"validation"},
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
		StopPolicy: promptiterengine.StopPolicy{
			TargetScore: &targetScore,
		},
	}
	cloned := cloneRunRequest(request)
	require.NotNil(t, cloned)
	cloned.TrainEvalSetIDs[0] = "mutated"
	cloned.ValidationEvalSetIDs[0] = "mutated"
	cloned.TargetSurfaceIDs[0] = "mutated"
	*cloned.InitialProfile.Overrides[0].Value.Text = "mutated"
	*cloned.StopPolicy.TargetScore = 1.0
	assert.Equal(t, "train", request.TrainEvalSetIDs[0])
	assert.Equal(t, "validation", request.ValidationEvalSetIDs[0])
	assert.Equal(t, "candidate#instruction", request.TargetSurfaceIDs[0])
	assert.Equal(t, "prompt", *request.InitialProfile.Overrides[0].Value.Text)
	assert.Equal(t, 0.9, *request.StopPolicy.TargetScore)
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
