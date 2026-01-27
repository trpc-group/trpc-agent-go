//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package local

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestLocalInferenceBeforeInferenceSetCanFilterEvalCaseIDs(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	require.NoError(t, err)

	case1 := makeEvalCase(appName, "case-1", "prompt-1")
	case1.EvalMode = evalset.EvalModeTrace
	case2 := makeEvalCase(appName, "case-2", "prompt-2")
	case2.EvalMode = evalset.EvalModeTrace
	require.NoError(t, mgr.AddCase(ctx, appName, evalSetID, case1))
	require.NoError(t, mgr.AddCase(ctx, appName, evalSetID, case2))

	callbacks := &service.Callbacks{}
	callbacks.Register("filter", &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			args.Request.EvalCaseIDs = []string{"case-2"}
			return nil, nil
		},
	})

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithCallbacks(callbacks),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "case-2", results[0].EvalCaseID)
}

func TestLocalInferenceBeforeInferenceSetContextUpdatesSessionIDSupplier(t *testing.T) {
	type sessionKey struct{}

	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	require.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.EvalMode = evalset.EvalModeTrace
	require.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

	callbacks := &service.Callbacks{}
	callbacks.Register("ctx", &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			next := context.WithValue(ctx, sessionKey{}, "session-from-callback")
			return &service.BeforeInferenceSetResult{Context: next}, nil
		},
	})

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithCallbacks(callbacks),
		service.WithSessionIDSupplier(func(ctx context.Context) string {
			if v, ok := ctx.Value(sessionKey{}).(string); ok {
				return v
			}
			return "missing"
		}),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "session-from-callback", results[0].SessionID)
}

func TestLocalInferenceBeforeInferenceCaseContextPropagatesToAfterInferenceCase(t *testing.T) {
	type caseKey struct{}

	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	require.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.EvalMode = evalset.EvalModeTrace
	require.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

	called := false
	callbacks := &service.Callbacks{}
	callbacks.Register("inject", &service.Callback{
		BeforeInferenceCase: func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
			next := context.WithValue(ctx, caseKey{}, "value")
			return &service.BeforeInferenceCaseResult{Context: next}, nil
		},
	})
	callbacks.Register("observe", &service.Callback{
		AfterInferenceCase: func(ctx context.Context, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
			called = true
			assert.Equal(t, "value", ctx.Value(caseKey{}))
			return nil, nil
		},
	})

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithCallbacks(callbacks),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, status.EvalStatusPassed, results[0].Status)
	assert.True(t, called)
}

func TestLocalInferenceBeforeInferenceCaseErrorMarksCaseFailed(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	require.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.EvalMode = evalset.EvalModeTrace
	require.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

	callbacks := &service.Callbacks{}
	callbacks.Register("fail", &service.Callback{
		BeforeInferenceCase: func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
			return nil, errors.New("before inference case failed")
		},
	})

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithCallbacks(callbacks),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
	assert.Contains(t, results[0].ErrorMessage, "before inference case failed")
	assert.Contains(t, results[0].ErrorMessage, "run before inference case callbacks")
}

func TestLocalInferenceAfterInferenceCaseErrorMarksCaseFailed(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	require.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.EvalMode = evalset.EvalModeTrace
	require.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

	callbacks := &service.Callbacks{}
	callbacks.Register("fail", &service.Callback{
		AfterInferenceCase: func(ctx context.Context, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
			return nil, errors.New("after inference case failed")
		},
	})

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithCallbacks(callbacks),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
	assert.Nil(t, results[0].Inferences)
	assert.Contains(t, results[0].ErrorMessage, "after inference case failed")
	assert.Contains(t, results[0].ErrorMessage, "run after inference case callbacks")
}

