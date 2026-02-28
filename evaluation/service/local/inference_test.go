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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestRunAfterInferenceSetCallbacksPassesArgs(t *testing.T) {
	ctx := context.Background()
	startTime := time.Unix(123, 0)
	wantErr := errors.New("inference error")
	req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
	results := []*service.InferenceResult{{AppName: "app", EvalSetID: "set"}}

	callbacks := service.NewCallbacks()
	var got *service.AfterInferenceSetArgs
	callbacks.RegisterAfterInferenceSet("probe", func(ctx context.Context, args *service.AfterInferenceSetArgs) (*service.AfterInferenceSetResult, error) {
		got = args
		return nil, nil
	})

	svc := &local{callbacks: callbacks}
	err := svc.runAfterInferenceSetCallbacks(ctx, callbacks, req, results, wantErr, startTime)
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Same(t, req, got.Request)
	assert.Len(t, got.Results, 1)
	assert.Same(t, results[0], got.Results[0])
	assert.Same(t, wantErr, got.Error)
	assert.Equal(t, startTime, got.StartTime)
}

func TestRunAfterInferenceSetCallbacksWrapsErrorWithContext(t *testing.T) {
	ctx := context.Background()
	startTime := time.Unix(123, 0)
	req := &service.InferenceRequest{AppName: "app", EvalSetID: "set"}
	sentinel := errors.New("boom")

	callbacks := service.NewCallbacks()
	callbacks.RegisterAfterInferenceSet("bad", func(ctx context.Context, args *service.AfterInferenceSetArgs) (*service.AfterInferenceSetResult, error) {
		return nil, sentinel
	})

	svc := &local{callbacks: callbacks}
	err := svc.runAfterInferenceSetCallbacks(ctx, callbacks, req, nil, nil, startTime)
	assert.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "run after inference set callbacks")
	assert.Contains(t, err.Error(), "app=app")
	assert.Contains(t, err.Error(), "evalSetID=set")
}

func TestLocalInferencePerCallSessionIDSupplierOverridesDefault(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.EvalMode = evalset.EvalModeTrace
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "default-session" }),
	)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(
		ctx,
		&service.InferenceRequest{AppName: appName, EvalSetID: evalSetID},
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "call-session" }),
	)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	if len(results) != 1 {
		return
	}
	assert.Equal(t, "call-session", results[0].SessionID)
}

func TestLocalInferencePerCallCallbacksOverrideDefault(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.EvalMode = evalset.EvalModeTrace
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	called := false
	callbacks := service.NewCallbacks()
	callbacks.RegisterBeforeInferenceSet("probe", func(ctx context.Context, args *service.BeforeInferenceSetArgs) (*service.BeforeInferenceSetResult, error) {
		called = true
		return nil, nil
	})

	_, err = svc.Inference(
		ctx,
		&service.InferenceRequest{AppName: appName, EvalSetID: evalSetID},
		service.WithCallbacks(callbacks),
	)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestLocalInferenceAfterInferenceCaseCallbackReceivesError(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

	callbacks := &service.Callbacks{}
	callbacks.Register("probe", &service.Callback{
		AfterInferenceCase: func(ctx context.Context, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
			assert.Error(t, args.Error)
			if args.Error != nil {
				assert.Contains(t, args.Error.Error(), "boom")
			}
			assert.NotNil(t, args.Result)
			if args.Result != nil {
				assert.Contains(t, args.Result.ErrorMessage, "boom")
			}
			return nil, nil
		},
	})

	svc, err := New(
		&fakeRunner{err: errors.New("boom")},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithCallbacks(callbacks),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	if len(results) != 1 {
		return
	}
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
}

func TestLocalInferenceBeforeInferenceSetCanFilterEvalCaseIDs(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	case1 := makeEvalCase(appName, "case-1", "prompt-1")
	case1.EvalMode = evalset.EvalModeTrace
	case2 := makeEvalCase(appName, "case-2", "prompt-2")
	case2.EvalMode = evalset.EvalModeTrace
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, case1))
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, case2))

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
	assert.NoError(t, err)
	if err != nil {
		return
	}
	if svc == nil {
		return
	}
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	if err != nil {
		return
	}
	assert.Len(t, results, 1)
	if len(results) != 1 {
		return
	}
	assert.Equal(t, "case-2", results[0].EvalCaseID)
}

func TestLocalInferenceBeforeInferenceSetContextUpdatesSessionIDSupplier(t *testing.T) {
	type sessionKey struct{}

	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.EvalMode = evalset.EvalModeTrace
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

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
	assert.NoError(t, err)
	if err != nil {
		return
	}
	if svc == nil {
		return
	}
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	if err != nil {
		return
	}
	assert.Len(t, results, 1)
	if len(results) != 1 {
		return
	}
	assert.Equal(t, "session-from-callback", results[0].SessionID)
}

func TestLocalInferenceBeforeInferenceCaseContextPropagatesToAfterInferenceCase(t *testing.T) {
	type caseKey struct{}

	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.EvalMode = evalset.EvalModeTrace
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

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
	assert.NoError(t, err)
	if err != nil {
		return
	}
	if svc == nil {
		return
	}
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	if err != nil {
		return
	}
	assert.Len(t, results, 1)
	if len(results) != 1 {
		return
	}
	assert.Equal(t, status.EvalStatusPassed, results[0].Status)
	assert.True(t, called)
}

func TestLocalInferenceBeforeInferenceCaseErrorMarksCaseFailed(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.EvalMode = evalset.EvalModeTrace
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

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
	assert.NoError(t, err)
	if err != nil {
		return
	}
	if svc == nil {
		return
	}
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	if err != nil {
		return
	}
	assert.Len(t, results, 1)
	if len(results) != 1 {
		return
	}
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
	assert.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.EvalMode = evalset.EvalModeTrace
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

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
	assert.NoError(t, err)
	if err != nil {
		return
	}
	if svc == nil {
		return
	}
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	if err != nil {
		return
	}
	assert.Len(t, results, 1)
	if len(results) != 1 {
		return
	}
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
	assert.NoError(t, err)
	if err != nil {
		return
	}
	if svc == nil {
		return
	}
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	_, err = svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.Error(t, err)
	if err == nil {
		return
	}
	assert.Contains(t, err.Error(), "run before inference set callbacks")
	assert.Contains(t, err.Error(), "before inference set failed")
}

func TestLocalInferenceEmptyConversationMarksCaseFailed(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.Conversation = nil
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	if svc == nil {
		return
	}
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	if err != nil {
		return
	}
	assert.Len(t, results, 1)
	if len(results) != 1 {
		return
	}
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
	assert.Nil(t, results[0].Inferences)
	assert.Contains(t, results[0].ErrorMessage, "invocations are empty")
}

type runOptionProbeRunner struct {
	events []*event.Event

	mu          sync.Mutex
	lastOptions agent.RunOptions
}

func (r *runOptionProbeRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	var opts agent.RunOptions
	for _, opt := range runOpts {
		opt(&opts)
	}

	r.mu.Lock()
	r.lastOptions = opts
	r.mu.Unlock()

	ch := make(chan *event.Event, len(r.events))
	for _, evt := range r.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (r *runOptionProbeRunner) Close() error {
	return nil
}

func TestLocalInferenceRunOptionsInjectionOrder(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.ContextMessages = []*model.Message{
		{Role: model.RoleSystem, Content: "case system"},
		{Role: model.RoleUser, Content: "case user"},
	}
	evalCase.SessionInput.State = map[string]any{
		"from_session": "yes",
	}
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))

	globalInjected := model.NewSystemMessage("global injected")
	overrideState := map[string]any{
		"from_run_option": "yes",
	}

	probeRunner := &runOptionProbeRunner{
		events: []*event.Event{makeFinalEvent("ok")},
	}
	svc, err := New(
		probeRunner,
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
		service.WithRunOptions(
			agent.WithInjectedContextMessages([]model.Message{globalInjected}),
			agent.WithRuntimeState(overrideState),
		),
	)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	defer func() { assert.NoError(t, svc.Close()) }()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, results, 1)

	probeRunner.mu.Lock()
	got := probeRunner.lastOptions
	probeRunner.mu.Unlock()

	expectedInjected := []model.Message{
		globalInjected,
		{Role: model.RoleSystem, Content: "case system"},
		{Role: model.RoleUser, Content: "case user"},
	}
	assert.Equal(t, expectedInjected, got.InjectedContextMessages)
	assert.Equal(t, evalCase.SessionInput.State, got.RuntimeState)
	assert.NotEqual(t, overrideState, got.RuntimeState)
}
