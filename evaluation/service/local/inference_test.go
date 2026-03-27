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
	"trpc.group/trpc-go/trpc-agent-go/agent/trace"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/usersimulation"
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

func TestLocalInferenceAfterInferenceCaseCallbackDoesNotReceivePerCaseError(t *testing.T) {
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
			assert.NoError(t, args.Error)
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

func TestLocalInference_FailedCasePreservesExecutionTraceArtifacts(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	evalCase := makeEvalCase(appName, "case-1", "prompt")
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))
	executionTrace := &trace.Trace{RootAgentName: "assistant", RootInvocationID: "inv-1"}
	runnerStub := &fakeRunner{events: []*event.Event{
		{
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{
					{Message: model.Message{Role: model.RoleAssistant, Content: "answer"}},
				},
			},
		},
		{
			Response: &model.Response{
				Error: &model.ResponseError{Message: "boom", Type: model.ErrorTypeAPIError},
			},
		},
		{
			InvocationID:   "generated-inv",
			ExecutionTrace: executionTrace,
			Response: &model.Response{
				Object: model.ObjectTypeRunnerCompletion,
				Done:   true,
			},
		},
	}}
	svc, err := New(
		runnerStub,
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
	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	if len(results) != 1 {
		return
	}
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
	assert.Len(t, results[0].ExecutionTraces, 1)
	if len(results[0].ExecutionTraces) != 1 {
		return
	}
	assert.Same(t, executionTrace, results[0].ExecutionTraces[0])
	assert.Nil(t, results[0].Inferences)
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

func TestLocalInferenceAfterInferenceCaseDoesNotReceiveInferenceError(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	evalCase := makeEvalCase(appName, "case-1", "prompt")
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))
	var gotErr error
	callbacks := &service.Callbacks{}
	callbacks.Register("observe", &service.Callback{
		AfterInferenceCase: func(ctx context.Context, args *service.AfterInferenceCaseArgs) (*service.AfterInferenceCaseResult, error) {
			gotErr = args.Error
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
	assert.NoError(t, gotErr)
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

func TestLocalInferenceRejectsNilEvalSetManagerOption(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

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
	defer func() { assert.NoError(t, svc.Close()) }()

	_, err = svc.Inference(
		ctx,
		&service.InferenceRequest{AppName: appName, EvalSetID: evalSetID},
		service.WithEvalSetManager(nil),
	)
	assert.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "eval set manager is nil")
	}
}

func TestLocalInferenceRejectsMissingSessionIDSupplierOption(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

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
	defer func() { assert.NoError(t, svc.Close()) }()

	_, err = svc.Inference(
		ctx,
		&service.InferenceRequest{AppName: appName, EvalSetID: evalSetID},
		service.WithSessionIDSupplier(nil),
	)
	assert.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "session id supplier is nil")
	}
}

func TestLocalInferenceRejectsInvalidParallelismOption(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

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
	defer func() { assert.NoError(t, svc.Close()) }()

	_, err = svc.Inference(
		ctx,
		&service.InferenceRequest{AppName: appName, EvalSetID: evalSetID},
		service.WithEvalCaseParallelInferenceEnabled(true),
		service.WithEvalCaseParallelism(0),
	)
	assert.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "eval case parallelism must be greater than 0")
	}
}

func TestLocalInferenceRejectsNilContextMessage(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	svc := &local{runner: &fakeRunner{}}
	req := &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID}
	evalCase := makeEvalCase(appName, "case-1", "prompt")
	evalCase.ContextMessages = []*model.Message{nil}
	opts := &service.Options{
		SessionIDSupplier: func(ctx context.Context) string { return "session" },
	}

	result := svc.inferenceEvalCase(ctx, req, evalCase, opts)

	assert.NotNil(t, result)
	if result == nil {
		return
	}
	assert.Equal(t, status.EvalStatusFailed, result.Status)
	assert.Contains(t, result.ErrorMessage, "context message is nil at index 0")
}

type scenarioTestConversation struct {
	decisions []*usersimulation.Decision
	requests  []*usersimulation.TurnRequest
	closed    bool
}

func (s *scenarioTestConversation) Next(ctx context.Context, req *usersimulation.TurnRequest) (*usersimulation.Decision, error) {
	s.requests = append(s.requests, req)
	if len(s.decisions) == 0 {
		return &usersimulation.Decision{Stop: true}, nil
	}
	decision := s.decisions[0]
	s.decisions = s.decisions[1:]
	return decision, nil
}

func (s *scenarioTestConversation) Close() error {
	s.closed = true
	return nil
}

type scenarioTestSimulator struct {
	startReq     *usersimulation.StartRequest
	conversation usersimulation.Conversation
}

func (s *scenarioTestSimulator) Start(ctx context.Context, req *usersimulation.StartRequest) (usersimulation.Conversation, error) {
	s.startReq = req
	return s.conversation, nil
}

func makeScenarioEvalCase(appName, caseID string) *evalset.EvalCase {
	return &evalset.EvalCase{
		EvalID: caseID,
		ConversationScenario: &evalset.ConversationScenario{
			ConversationPlan: "Ask clarifying questions until the goal is done.",
			StopSignal:       "</finished>",
		},
		SessionInput: &evalset.SessionInput{
			AppName: appName,
			UserID:  "demo-user",
			State:   map[string]any{"locale": "zh-CN"},
		},
	}
}

func TestInferenceEvalCaseConversationScenarioRequiresUserSimulator(t *testing.T) {
	svc := &local{
		runner:            &fakeRunner{},
		sessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
	}
	result := svc.inferenceEvalCase(
		context.Background(),
		&service.InferenceRequest{AppName: "app", EvalSetID: "set"},
		makeScenarioEvalCase("app", "case-1"),
		&service.Options{SessionIDSupplier: func(ctx context.Context) string { return "session-scenario" }},
	)
	assert.Equal(t, status.EvalStatusFailed, result.Status)
	assert.Contains(t, result.ErrorMessage, "user simulator is nil")
}

func TestInferenceEvalCaseConversationScenarioRejectsMixedInputs(t *testing.T) {
	sim := &scenarioTestSimulator{conversation: &scenarioTestConversation{}}
	svc := &local{
		runner:            &fakeRunner{},
		sessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
		userSimulator:     sim,
	}
	evalCase := makeScenarioEvalCase("app", "case-1")
	evalCase.Conversation = []*evalset.Invocation{makeInvocation("inv-1", "prompt")}
	result := svc.inferenceEvalCase(
		context.Background(),
		&service.InferenceRequest{AppName: "app", EvalSetID: "set"},
		evalCase,
		&service.Options{
			SessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
			UserSimulator:     sim,
		},
	)
	assert.Equal(t, status.EvalStatusFailed, result.Status)
	assert.Contains(t, result.ErrorMessage, "cannot both be configured")
}

func TestInferenceEvalCaseConversationScenarioRejectsTraceMode(t *testing.T) {
	sim := &scenarioTestSimulator{conversation: &scenarioTestConversation{}}
	svc := &local{
		runner:            &fakeRunner{},
		sessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
		userSimulator:     sim,
	}
	evalCase := makeScenarioEvalCase("app", "case-1")
	evalCase.EvalMode = evalset.EvalModeTrace
	result := svc.inferenceEvalCase(
		context.Background(),
		&service.InferenceRequest{AppName: "app", EvalSetID: "set"},
		evalCase,
		&service.Options{
			SessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
			UserSimulator:     sim,
		},
	)
	assert.Equal(t, status.EvalStatusFailed, result.Status)
	assert.Contains(t, result.ErrorMessage, "conversationScenario is not supported in trace mode")
}

func TestInferenceEvalCaseConversationScenarioSuccess(t *testing.T) {
	executionTrace := &trace.Trace{RootAgentName: "assistant", RootInvocationID: "generated-inv"}
	conversation := &scenarioTestConversation{
		decisions: []*usersimulation.Decision{
			{Message: &model.Message{Role: model.RoleUser, Content: "Please book tomorrow morning."}},
			{Stop: true},
		},
	}
	simulator := &scenarioTestSimulator{conversation: conversation}
	svc := &local{
		runner: &fakeRunner{events: []*event.Event{
			makeFinalEvent("Booked."),
			{
				InvocationID:   "generated-invocation",
				ExecutionTrace: executionTrace,
				Response: &model.Response{
					Object: model.ObjectTypeRunnerCompletion,
					Done:   true,
				},
			},
		}},
		sessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
		userSimulator:     simulator,
	}
	result := svc.inferenceEvalCase(
		context.Background(),
		&service.InferenceRequest{AppName: "app", EvalSetID: "set"},
		makeScenarioEvalCase("app", "case-1"),
		&service.Options{
			SessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
			UserSimulator:     simulator,
		},
	)
	assert.Equal(t, status.EvalStatusPassed, result.Status)
	assert.Len(t, result.Inferences, 1)
	if assert.Len(t, result.ExecutionTraces, 1) {
		assert.Same(t, executionTrace, result.ExecutionTraces[0])
	}
	assert.True(t, conversation.closed)
	if assert.NotNil(t, simulator.startReq) {
		assert.Equal(t, "case-1", simulator.startReq.EvalCaseID)
		assert.Equal(t, "session-scenario", simulator.startReq.SessionID)
	}
	if assert.Len(t, result.Inferences, 1) {
		if assert.NotNil(t, result.Inferences[0].UserContent) {
			assert.Equal(t, "Please book tomorrow morning.", result.Inferences[0].UserContent.Content)
		}
		if assert.NotNil(t, result.Inferences[0].FinalResponse) {
			assert.Equal(t, "Booked.", result.Inferences[0].FinalResponse.Content)
		}
	}
}

func TestInferenceEvalCaseConversationScenarioExpectedDriverRequiresExpectedRunner(t *testing.T) {
	sim := &scenarioTestSimulator{conversation: &scenarioTestConversation{}}
	svc := &local{
		runner:            &fakeRunner{},
		sessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
		userSimulator:     sim,
	}
	evalCase := makeScenarioEvalCase("app", "case-1")
	evalCase.ConversationScenario.Driver = evalset.ConversationScenarioDriverExpected
	result := svc.inferenceEvalCase(
		context.Background(),
		&service.InferenceRequest{AppName: "app", EvalSetID: "set"},
		evalCase,
		&service.Options{
			SessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
			UserSimulator:     sim,
		},
	)
	assert.Equal(t, status.EvalStatusFailed, result.Status)
	assert.Contains(t, result.ErrorMessage, "expected runner is nil")
}

func TestInferenceEvalCaseConversationScenarioExpectedDriverUsesExpectedRunner(t *testing.T) {
	conversation := &scenarioTestConversation{
		decisions: []*usersimulation.Decision{
			{Message: &model.Message{Role: model.RoleUser, Content: "Please book tomorrow morning."}},
			{Message: &model.Message{Role: model.RoleUser, Content: "I prefer a window seat."}},
			{Stop: true},
		},
	}
	simulator := &scenarioTestSimulator{conversation: conversation}
	actualRunner := &fakeRunner{events: []*event.Event{makeFinalEvent("actual")}}
	expectedRunner := &fakeRunner{events: []*event.Event{makeFinalEvent("expected")}}
	svc := &local{
		runner:            actualRunner,
		expectedRunner:    expectedRunner,
		sessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
		userSimulator:     simulator,
	}
	evalCase := makeScenarioEvalCase("app", "case-1")
	evalCase.ConversationScenario.Driver = evalset.ConversationScenarioDriverExpected
	evalCase.ExpectedRunnerEnabled = true
	result := svc.inferenceEvalCase(
		context.Background(),
		&service.InferenceRequest{AppName: "app", EvalSetID: "set"},
		evalCase,
		&service.Options{
			SessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
			ExpectedRunner:    expectedRunner,
			UserSimulator:     simulator,
		},
	)
	assert.Equal(t, status.EvalStatusPassed, result.Status)
	assert.Len(t, result.Inferences, 2)
	assert.Len(t, result.ExpectedInferences, 2)
	assert.True(t, conversation.closed)
	if assert.Len(t, conversation.requests, 3) {
		assert.Nil(t, conversation.requests[0].LastTargetResponse)
		if assert.NotNil(t, conversation.requests[1].LastTargetResponse) {
			assert.Equal(t, "expected", conversation.requests[1].LastTargetResponse.Content)
		}
		if assert.NotNil(t, conversation.requests[2].LastTargetResponse) {
			assert.Equal(t, "expected", conversation.requests[2].LastTargetResponse.Content)
		}
	}
	if assert.Len(t, result.Inferences, 2) {
		assert.Equal(t, "Please book tomorrow morning.", result.Inferences[0].UserContent.Content)
		assert.Equal(t, "I prefer a window seat.", result.Inferences[1].UserContent.Content)
		assert.Equal(t, "actual", result.Inferences[0].FinalResponse.Content)
		assert.Equal(t, "actual", result.Inferences[1].FinalResponse.Content)
	}
	if assert.Len(t, result.ExpectedInferences, 2) {
		assert.Equal(t, "expected", result.ExpectedInferences[0].FinalResponse.Content)
		assert.Equal(t, "expected", result.ExpectedInferences[1].FinalResponse.Content)
	}
	expectedRunner.mu.Lock()
	expectedSessionIDs := append([]string(nil), expectedRunner.sessionIDs...)
	expectedRunner.mu.Unlock()
	assert.Equal(t, []string{"session-scenario-expected", "session-scenario-expected"}, expectedSessionIDs)
	actualRunner.mu.Lock()
	actualSessionIDs := append([]string(nil), actualRunner.sessionIDs...)
	actualRunner.mu.Unlock()
	assert.Equal(t, []string{"session-scenario", "session-scenario"}, actualSessionIDs)
}

func TestInferenceEvalCaseConversationScenarioExpectedDriverWithoutExpectedRunnerEnabledDoesNotExposeExpecteds(t *testing.T) {
	conversation := &scenarioTestConversation{
		decisions: []*usersimulation.Decision{
			{Message: &model.Message{Role: model.RoleUser, Content: "Please book tomorrow morning."}},
			{Message: &model.Message{Role: model.RoleUser, Content: "I prefer a window seat."}},
			{Stop: true},
		},
	}
	simulator := &scenarioTestSimulator{conversation: conversation}
	actualRunner := &fakeRunner{events: []*event.Event{makeFinalEvent("actual")}}
	expectedRunner := &fakeRunner{events: []*event.Event{makeFinalEvent("expected")}}
	svc := &local{
		runner:            actualRunner,
		expectedRunner:    expectedRunner,
		sessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
		userSimulator:     simulator,
	}
	evalCase := makeScenarioEvalCase("app", "case-1")
	evalCase.ConversationScenario.Driver = evalset.ConversationScenarioDriverExpected
	evalCase.ExpectedRunnerEnabled = false
	result := svc.inferenceEvalCase(
		context.Background(),
		&service.InferenceRequest{AppName: "app", EvalSetID: "set"},
		evalCase,
		&service.Options{
			SessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
			ExpectedRunner:    expectedRunner,
			UserSimulator:     simulator,
		},
	)
	assert.Equal(t, status.EvalStatusPassed, result.Status)
	assert.Len(t, result.Inferences, 2)
	assert.Nil(t, result.ExpectedInferences)
	expectedRunner.mu.Lock()
	expectedCalls := len(expectedRunner.calls)
	expectedRunner.mu.Unlock()
	assert.Equal(t, 2, expectedCalls)
}

func TestInferenceEvalCaseConversationScenarioExpectedDriverFailureWithoutExpectedRunnerEnabledDoesNotExposeExpecteds(t *testing.T) {
	conversation := &scenarioTestConversation{
		decisions: []*usersimulation.Decision{
			{Message: &model.Message{Role: model.RoleUser, Content: "Please book tomorrow morning."}},
			{Stop: true},
		},
	}
	simulator := &scenarioTestSimulator{conversation: conversation}
	actualRunner := &fakeRunner{err: errors.New("actual failed")}
	expectedRunner := &fakeRunner{events: []*event.Event{makeFinalEvent("expected")}}
	svc := &local{
		runner:            actualRunner,
		expectedRunner:    expectedRunner,
		sessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
		userSimulator:     simulator,
	}
	evalCase := makeScenarioEvalCase("app", "case-1")
	evalCase.ConversationScenario.Driver = evalset.ConversationScenarioDriverExpected
	evalCase.ExpectedRunnerEnabled = false
	result := svc.inferenceEvalCase(
		context.Background(),
		&service.InferenceRequest{AppName: "app", EvalSetID: "set"},
		evalCase,
		&service.Options{
			SessionIDSupplier: func(ctx context.Context) string { return "session-scenario" },
			ExpectedRunner:    expectedRunner,
			UserSimulator:     simulator,
		},
	)
	assert.Equal(t, status.EvalStatusFailed, result.Status)
	assert.Contains(t, result.ErrorMessage, "actual failed")
	assert.Nil(t, result.ExpectedInferences)
}
