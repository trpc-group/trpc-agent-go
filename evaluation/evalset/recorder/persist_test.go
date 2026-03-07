//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package recorder

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type stubManager struct {
	getFn        func(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error)
	createFn     func(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error)
	listFn       func(ctx context.Context, appName string) ([]string, error)
	deleteFn     func(ctx context.Context, appName, evalSetID string) error
	getCaseFn    func(ctx context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error)
	addCaseFn    func(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error
	updateCaseFn func(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error
	deleteCaseFn func(ctx context.Context, appName, evalSetID, evalCaseID string) error
	closeFn      func() error
}

func (m *stubManager) Get(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	if m.getFn != nil {
		return m.getFn(ctx, appName, evalSetID)
	}
	return nil, os.ErrNotExist
}

func (m *stubManager) Create(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	if m.createFn != nil {
		return m.createFn(ctx, appName, evalSetID)
	}
	return &evalset.EvalSet{EvalSetID: evalSetID}, nil
}

func (m *stubManager) List(ctx context.Context, appName string) ([]string, error) {
	if m.listFn != nil {
		return m.listFn(ctx, appName)
	}
	return nil, nil
}

func (m *stubManager) Delete(ctx context.Context, appName, evalSetID string) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, appName, evalSetID)
	}
	return nil
}

func (m *stubManager) GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	if m.getCaseFn != nil {
		return m.getCaseFn(ctx, appName, evalSetID, evalCaseID)
	}
	return nil, os.ErrNotExist
}

func (m *stubManager) AddCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	if m.addCaseFn != nil {
		return m.addCaseFn(ctx, appName, evalSetID, evalCase)
	}
	return nil
}

func (m *stubManager) UpdateCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	if m.updateCaseFn != nil {
		return m.updateCaseFn(ctx, appName, evalSetID, evalCase)
	}
	return nil
}

func (m *stubManager) DeleteCase(ctx context.Context, appName, evalSetID, evalCaseID string) error {
	if m.deleteCaseFn != nil {
		return m.deleteCaseFn(ctx, appName, evalSetID, evalCaseID)
	}
	return nil
}

func (m *stubManager) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

func TestPersist_FunctionBranches(t *testing.T) {
	t.Run("sort and conversation helpers", func(t *testing.T) {
		now := time.Unix(20, 0)
		later := time.Unix(30, 0)
		invocations := []*evalset.Invocation{
			{InvocationID: "b", CreationTimestamp: &epochtime.EpochTime{Time: now}},
			nil,
			{InvocationID: "a", CreationTimestamp: &epochtime.EpochTime{Time: now}},
			{InvocationID: "c", CreationTimestamp: &epochtime.EpochTime{Time: later}},
		}
		sortInvocations(invocations)
		require.Nil(t, conversationByMode(nil, evalset.EvalModeDefault))
		assert.Equal(t, time.Time{}, invocationTime(nil))
		assert.Equal(t, time.Time{}, invocationTime(&evalset.Invocation{}))
		assert.True(t, hasInvocation(invocations, "a"))
		assert.False(t, hasInvocation(invocations, "missing"))
		assert.False(t, hasInvocation(invocations, ""))
		assert.Nil(t, invocations[0])
		assert.Equal(t, "a", invocations[1].InvocationID)
		assert.Equal(t, "b", invocations[2].InvocationID)
		assert.Equal(t, "c", invocations[3].InvocationID)
		evalCase := &evalset.EvalCase{}
		appendConversationByMode(nil, evalset.EvalModeDefault, &evalset.Invocation{InvocationID: "ignored"})
		appendConversationByMode(evalCase, evalset.EvalModeDefault, &evalset.Invocation{InvocationID: "default"})
		appendConversationByMode(evalCase, evalset.EvalModeTrace, &evalset.Invocation{InvocationID: "trace"})
		require.Len(t, conversationByMode(evalCase, evalset.EvalModeDefault), 1)
		require.Len(t, conversationByMode(evalCase, evalset.EvalModeTrace), 1)
	})
	t.Run("ensure eval set paths", func(t *testing.T) {
		rec := &Recorder{manager: &stubManager{getFn: func(context.Context, string, string) (*evalset.EvalSet, error) {
			return &evalset.EvalSet{EvalSetID: "set"}, nil
		}}, locker: newKeyedLocker()}
		require.NoError(t, rec.ensureEvalSet(context.Background(), "app", "set"))
		rec = &Recorder{manager: &stubManager{getFn: func(context.Context, string, string) (*evalset.EvalSet, error) { return nil, errors.New("boom") }}, locker: newKeyedLocker()}
		require.ErrorContains(t, rec.ensureEvalSet(context.Background(), "app", "set"), "get eval set app.set")
		callCount := 0
		rec = &Recorder{manager: &stubManager{
			getFn: func(context.Context, string, string) (*evalset.EvalSet, error) {
				callCount++
				if callCount == 1 {
					return nil, os.ErrNotExist
				}
				return &evalset.EvalSet{EvalSetID: "set"}, nil
			},
			createFn: func(context.Context, string, string) (*evalset.EvalSet, error) {
				return nil, errors.New("already exists")
			},
		}, locker: newKeyedLocker()}
		require.NoError(t, rec.ensureEvalSet(context.Background(), "app", "set"))
		rec = &Recorder{manager: &stubManager{
			getFn:    func(context.Context, string, string) (*evalset.EvalSet, error) { return nil, os.ErrNotExist },
			createFn: func(context.Context, string, string) (*evalset.EvalSet, error) { return nil, errors.New("create boom") },
		}, locker: newKeyedLocker()}
		require.ErrorContains(t, rec.ensureEvalSet(context.Background(), "app", "set"), "create eval set app.set")
	})
	t.Run("append invocation paths", func(t *testing.T) {
		baseTurn := &turnToPersist{
			appName:    "app",
			evalSetID:  "set",
			evalCaseID: "case",
			sessionIn:  &evalset.SessionInput{AppName: "app"},
			invocation: &evalset.Invocation{InvocationID: "req-2", CreationTimestamp: &epochtime.EpochTime{Time: time.Unix(20, 0)}},
		}
		var added *evalset.EvalCase
		rec := &Recorder{manager: &stubManager{
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) { return nil, os.ErrNotExist },
			addCaseFn: func(_ context.Context, _, _ string, evalCase *evalset.EvalCase) error {
				added = evalCase
				return nil
			},
		}, locker: newKeyedLocker()}
		require.NoError(t, rec.appendInvocation(context.Background(), baseTurn))
		require.NotNil(t, added)
		require.Len(t, added.Conversation, 1)
		rec = &Recorder{manager: &stubManager{
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) { return nil, os.ErrNotExist },
			addCaseFn: func(context.Context, string, string, *evalset.EvalCase) error { return errors.New("add boom") },
		}, locker: newKeyedLocker()}
		require.ErrorContains(t, rec.appendInvocation(context.Background(), baseTurn), "add eval case app.set.case")
		rec = &Recorder{manager: &stubManager{
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) {
				return nil, errors.New("get boom")
			},
		}, locker: newKeyedLocker()}
		require.ErrorContains(t, rec.appendInvocation(context.Background(), baseTurn), "get eval case app.set.case")
		rec = &Recorder{manager: &stubManager{
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) {
				return &evalset.EvalCase{EvalMode: evalset.EvalModeTrace}, nil
			},
		}, locker: newKeyedLocker()}
		require.ErrorContains(t, rec.appendInvocation(context.Background(), baseTurn), "mode mismatch")
		updateCalls := 0
		existing := &evalset.EvalCase{
			EvalID:       "case",
			SessionInput: nil,
			Conversation: []*evalset.Invocation{{InvocationID: "req-1", CreationTimestamp: &epochtime.EpochTime{Time: time.Unix(10, 0)}}},
		}
		rec = &Recorder{manager: &stubManager{
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) { return existing, nil },
			updateCaseFn: func(_ context.Context, _, _ string, evalCase *evalset.EvalCase) error {
				updateCalls++
				existing = evalCase
				return nil
			},
		}, locker: newKeyedLocker()}
		turn := &turnToPersist{
			appName:         "app",
			evalSetID:       "set",
			evalCaseID:      "case",
			sessionIn:       &evalset.SessionInput{AppName: "app", UserID: "u-1"},
			contextMessages: []*model.Message{{Role: model.RoleSystem, Content: "ctx"}},
			invocation:      &evalset.Invocation{InvocationID: "req-2", CreationTimestamp: &epochtime.EpochTime{Time: time.Unix(20, 0)}},
		}
		require.NoError(t, rec.appendInvocation(context.Background(), turn))
		require.Equal(t, 1, updateCalls)
		require.NotNil(t, existing.SessionInput)
		require.Len(t, existing.ContextMessages, 1)
		require.Len(t, existing.Conversation, 2)
		assert.Equal(t, "req-1", existing.Conversation[0].InvocationID)
		assert.Equal(t, "req-2", existing.Conversation[1].InvocationID)
		require.NoError(t, rec.appendInvocation(context.Background(), turn))
		require.Equal(t, 1, updateCalls)
		rec = &Recorder{manager: &stubManager{
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) {
				return &evalset.EvalCase{}, nil
			},
			updateCaseFn: func(context.Context, string, string, *evalset.EvalCase) error { return errors.New("update boom") },
		}, locker: newKeyedLocker()}
		require.ErrorContains(t, rec.appendInvocation(context.Background(), &turnToPersist{
			appName:    "app",
			evalSetID:  "set",
			evalCaseID: "case",
			invocation: &evalset.Invocation{InvocationID: "req-3"},
		}), "update eval case app.set.case")
	})
	t.Run("persist turn context paths", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		rec := &Recorder{manager: &stubManager{}, locker: newKeyedLocker()}
		err := rec.persistTurn(ctx, &turnToPersist{appName: "app", evalSetID: "set", evalCaseID: "case"})
		require.ErrorIs(t, err, context.Canceled)
		rec = &Recorder{manager: &stubManager{
			getFn: func(context.Context, string, string) (*evalset.EvalSet, error) {
				return &evalset.EvalSet{EvalSetID: "set"}, nil
			},
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) { return nil, os.ErrNotExist },
		}, locker: newKeyedLocker()}
		require.NoError(t, rec.persistTurn(context.Background(), &turnToPersist{
			appName:    "app",
			evalSetID:  "set",
			evalCaseID: "case",
			invocation: &evalset.Invocation{InvocationID: "req"},
		}))
	})
}
