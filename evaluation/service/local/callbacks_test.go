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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
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

func TestLocalInferenceBeforeInferenceSetErrorReturnsError(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	callbacks := &service.Callbacks{}
	callbacks.Register("fail", &service.Callback{
		BeforeInferenceSet: func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
			return nil, errors.New("before inference set failed")
		},
	})

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(evalsetinmemory.New()),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithCallbacks(callbacks),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	_, err = svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "run before inference set callbacks")
	assert.Contains(t, err.Error(), "before inference set failed")
}

func TestLocalInferenceEmptyConversationMarksCaseFailed(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	require.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.Conversation = nil
	require.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
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
	assert.Contains(t, results[0].ErrorMessage, "invocations are empty")
}

func TestLocalEvaluateBeforeEvaluateSetContextPropagatesToAfterEvaluateSet(t *testing.T) {
	type evalSetKey struct{}

	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	called := false
	callbacks := &service.Callbacks{}
	callbacks.Register("observe", &service.Callback{
		BeforeEvaluateSet: func(ctx context.Context, args *service.BeforeEvaluateSetArgs) (*service.BeforeEvaluateSetResult, error) {
			next := context.WithValue(ctx, evalSetKey{}, "value")
			return &service.BeforeEvaluateSetResult{Context: next}, nil
		},
		AfterEvaluateSet: func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
			called = true
			assert.Equal(t, "value", ctx.Value(evalSetKey{}))
			return nil, nil
		},
	})

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(evalsetinmemory.New()),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithCallbacks(callbacks),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-1", SessionID: "session", Status: status.EvalStatusFailed, ErrorMessage: "failed"}},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	require.NoError(t, err)
	assert.True(t, called)
}

func TestLocalEvaluateBeforeEvaluateSetErrorReturnsError(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	callbacks := &service.Callbacks{}
	callbacks.Register("fail", &service.Callback{
		BeforeEvaluateSet: func(ctx context.Context, args *service.BeforeEvaluateSetArgs) (*service.BeforeEvaluateSetResult, error) {
			return nil, errors.New("before evaluate set failed")
		},
	})

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(evalsetinmemory.New()),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithCallbacks(callbacks),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:        appName,
		EvalSetID:      evalSetID,
		EvaluateConfig: &service.EvaluateConfig{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "run before evaluate set callbacks")
	assert.Contains(t, err.Error(), "before evaluate set failed")
}

func TestLocalEvaluateBeforeEvaluateCaseContextPropagatesToAfterEvaluateCase(t *testing.T) {
	type evalCaseKey struct{}

	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	called := false
	callbacks := &service.Callbacks{}
	callbacks.Register("inject", &service.Callback{
		BeforeEvaluateCase: func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
			next := context.WithValue(ctx, evalCaseKey{}, "value")
			return &service.BeforeEvaluateCaseResult{Context: next}, nil
		},
	})
	callbacks.Register("observe", &service.Callback{
		AfterEvaluateCase: func(ctx context.Context, args *service.AfterEvaluateCaseArgs) (*service.AfterEvaluateCaseResult, error) {
			called = true
			assert.Equal(t, "value", ctx.Value(evalCaseKey{}))
			return nil, nil
		},
	})

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(evalsetinmemory.New()),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithCallbacks(callbacks),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-1", SessionID: "session", Status: status.EvalStatusFailed, ErrorMessage: "failed"}},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	require.NoError(t, err)
	assert.True(t, called)
}

func TestLocalEvaluateBeforeEvaluateCaseErrorReturnsError(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	callbacks := &service.Callbacks{}
	callbacks.Register("fail", &service.Callback{
		BeforeEvaluateCase: func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
			return nil, errors.New("before evaluate case failed")
		},
	})

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(evalsetinmemory.New()),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithCallbacks(callbacks),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-1", SessionID: "session", Status: status.EvalStatusFailed, ErrorMessage: "failed"}},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "evaluate case")
	assert.Contains(t, err.Error(), "run before evaluate case callbacks")
	assert.Contains(t, err.Error(), "before evaluate case failed")
}

func TestLocalEvaluateAfterEvaluateCaseErrorReturnsError(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	callbacks := &service.Callbacks{}
	callbacks.Register("fail", &service.Callback{
		AfterEvaluateCase: func(ctx context.Context, args *service.AfterEvaluateCaseArgs) (*service.AfterEvaluateCaseResult, error) {
			return nil, errors.New("after evaluate case failed")
		},
	})

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(evalsetinmemory.New()),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithCallbacks(callbacks),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-1", SessionID: "session", Status: status.EvalStatusFailed, ErrorMessage: "failed"}},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "run after evaluate case callbacks")
	assert.Contains(t, err.Error(), "after evaluate case failed")
}

func TestLocalRunAfterEvaluateCaseCallbacksNilInferenceResultIncludesEmptyEvalCaseID(t *testing.T) {
	ctx := context.Background()

	callbacks := &service.Callbacks{}
	callbacks.Register("fail", &service.Callback{
		AfterEvaluateCase: func(ctx context.Context, args *service.AfterEvaluateCaseArgs) (*service.AfterEvaluateCaseResult, error) {
			return nil, errors.New("after evaluate case failed")
		},
	})

	svc := &local{callbacks: callbacks}
	err := svc.runAfterEvaluateCaseCallbacks(ctx, &service.EvaluateRequest{AppName: "app", EvalSetID: "set", EvaluateConfig: &service.EvaluateConfig{}}, nil, nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "run after evaluate case callbacks")
	assert.Contains(t, err.Error(), "evalCaseID=")
}

func TestLocalEvaluateNilInferenceResultReturnsErrorWithEmptyEvalCaseID(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(evalsetinmemory.New()),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{nil},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "evaluate case")
	assert.Contains(t, err.Error(), "evalCaseID=")
	assert.Contains(t, err.Error(), "inference result is nil")
}

func TestLocalEvaluateSaveEvalSetResultErrorReturnsError(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(evalsetinmemory.New()),
		service.WithEvalResultManager(&failingEvalResultManager{err: errors.New("save failed")}),
		service.WithRegistry(registry.New()),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-1", SessionID: "session", Status: status.EvalStatusFailed, ErrorMessage: "failed"}},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "save eval set result")
	assert.Contains(t, err.Error(), "save failed")
}

func TestLocalEvaluatePerCaseErrorMarksCaseFailed(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := failingEvalSetManager{
		Manager: evalsetinmemory.New(),
		err:     errors.New("get case failed"),
	}

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	result, err := svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-1", SessionID: "session", Status: status.EvalStatusPassed}},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	require.NoError(t, err)
	require.Len(t, result.EvalCaseResults, 1)
	assert.Equal(t, status.EvalStatusFailed, result.EvalCaseResults[0].FinalEvalStatus)
	assert.Contains(t, result.EvalCaseResults[0].ErrorMessage, "get eval case")
	assert.Contains(t, result.EvalCaseResults[0].ErrorMessage, "get case failed")
}

type failingEvalResultManager struct {
	err error
}

func (m *failingEvalResultManager) Save(ctx context.Context, appName string, evalSetResult *evalresult.EvalSetResult) (string, error) {
	return "", m.err
}

func (m *failingEvalResultManager) Get(ctx context.Context, appName, evalSetResultID string) (*evalresult.EvalSetResult, error) {
	return nil, nil
}

func (m *failingEvalResultManager) List(ctx context.Context, appName string) ([]string, error) {
	return nil, nil
}

type failingEvalSetManager struct {
	evalset.Manager
	err error
}

func (m failingEvalSetManager) GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	return nil, m.err
}
