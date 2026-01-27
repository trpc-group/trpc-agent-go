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
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type fakeRunner struct {
	events []*event.Event
	err    error

	mu    sync.Mutex
	calls []model.Message
}

func (f *fakeRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	f.calls = append(f.calls, message)
	f.mu.Unlock()

	ch := make(chan *event.Event, len(f.events))
	for _, evt := range f.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (f *fakeRunner) Close() error {
	return nil
}

type controlledRunner struct {
	started       chan string
	finished      chan string
	fastRelease   chan struct{}
	slowRelease   chan struct{}
	running       int32
	maxConcurrent int32
}

func (c *controlledRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	cur := atomic.AddInt32(&c.running, 1)
	for {
		prev := atomic.LoadInt32(&c.maxConcurrent)
		if cur <= prev {
			break
		}
		if atomic.CompareAndSwapInt32(&c.maxConcurrent, prev, cur) {
			break
		}
	}
	c.started <- message.Content
	if message.Content == "fast" {
		<-c.fastRelease
	} else {
		<-c.slowRelease
	}
	c.finished <- message.Content
	atomic.AddInt32(&c.running, -1)

	ch := make(chan *event.Event, 1)
	ch <- makeFinalEvent("resp:" + message.Content)
	close(ch)
	return ch, nil
}

func (c *controlledRunner) Close() error {
	return nil
}

type fakeEvaluator struct {
	name   string
	result *evaluator.EvaluateResult
	err    error

	receivedActuals   []*evalset.Invocation
	receivedExpecteds []*evalset.Invocation
}

func (f *fakeEvaluator) Name() string {
	return f.name
}

func (f *fakeEvaluator) Description() string {
	return "fake evaluator"
}

func (f *fakeEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.receivedActuals = actuals
	f.receivedExpecteds = expecteds
	return f.result, nil
}

func makeFinalEvent(text string) *event.Event {
	return &event.Event{
		InvocationID: "generated-invocation",
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{
					Message: model.Message{Role: model.RoleAssistant, Content: text},
				},
			},
		},
	}
}

func makeInvocation(id, prompt string) *evalset.Invocation {
	return &evalset.Invocation{
		InvocationID: id,
		UserContent: &model.Message{
			Role:    model.RoleUser,
			Content: prompt,
		},
	}
}

func makeActualInvocation(id, prompt, response string) *evalset.Invocation {
	inv := makeInvocation(id, prompt)
	inv.FinalResponse = &model.Message{
		Role:    model.RoleAssistant,
		Content: response,
	}
	return inv
}

func makeEvalCase(appName, caseID, prompt string) *evalset.EvalCase {
	return &evalset.EvalCase{
		EvalID: caseID,
		Conversation: []*evalset.Invocation{
			makeInvocation(caseID+"-1", prompt),
		},
		SessionInput: &evalset.SessionInput{
			AppName: appName,
			UserID:  "demo-user",
			State:   map[string]any{},
		},
	}
}

func makeInferenceResult(appName, evalSetID, caseID, sessionID string, inferences []*evalset.Invocation) *service.InferenceResult {
	return &service.InferenceResult{
		AppName:    appName,
		EvalSetID:  evalSetID,
		EvalCaseID: caseID,
		Inferences: inferences,
		SessionID:  sessionID,
		Status:     status.EvalStatusPassed,
	}
}

func newLocalService(t *testing.T, r runner.Runner, evalSetMgr evalset.Manager, resultMgr evalresult.Manager, reg registry.Registry, sessionID string) *local {
	t.Helper()
	svc, err := New(
		r,
		service.WithEvalSetManager(evalSetMgr),
		service.WithEvalResultManager(resultMgr),
		service.WithRegistry(reg),
		service.WithSessionIDSupplier(func(ctx context.Context) string {
			return sessionID
		}),
	)
	assert.NoError(t, err)
	l, ok := svc.(*local)
	assert.True(t, ok)
	return l
}

func TestLocalNewValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		r       runner.Runner
		options []service.Option
		wantErr string
	}{
		{
			name:    "nil_runner",
			r:       nil,
			wantErr: "runner is nil",
		},
		{
			name: "parallel_inference_requires_positive_parallelism",
			r:    &fakeRunner{},
			options: []service.Option{
				service.WithEvalCaseParallelInferenceEnabled(true),
				service.WithEvalCaseParallelism(0),
			},
			wantErr: "eval case parallelism must be greater than 0",
		},
		{
			name: "nil_eval_set_manager",
			r:    &fakeRunner{},
			options: []service.Option{
				service.WithEvalSetManager(nil),
			},
			wantErr: "eval set manager is nil",
		},
		{
			name: "nil_eval_result_manager",
			r:    &fakeRunner{},
			options: []service.Option{
				service.WithEvalResultManager(nil),
			},
			wantErr: "eval result manager is nil",
		},
		{
			name: "nil_registry",
			r:    &fakeRunner{},
			options: []service.Option{
				service.WithRegistry(nil),
			},
			wantErr: "registry is nil",
		},
		{
			name: "nil_session_id_supplier",
			r:    &fakeRunner{},
			options: []service.Option{
				service.WithSessionIDSupplier(nil),
			},
			wantErr: "session id supplier is nil",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := New(tc.r, tc.options...)
			assert.Error(t, err)
			assert.Nil(t, svc)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestLocalNewDefaultOptions(t *testing.T) {
	svc, err := New(&fakeRunner{})
	assert.NoError(t, err)
	assert.NotNil(t, svc)

	localSvc, ok := svc.(*local)
	assert.True(t, ok)
	assert.False(t, localSvc.evalCaseParallelInferenceEnabled)
	assert.Nil(t, localSvc.evalCaseInferencePool)

	assert.NoError(t, svc.Close())
}

func TestLocalNewParallelInferenceCreatesPool(t *testing.T) {
	svc, err := New(
		&fakeRunner{},
		service.WithEvalCaseParallelInferenceEnabled(true),
		service.WithEvalCaseParallelism(2),
	)
	assert.NoError(t, err)
	assert.NotNil(t, svc)

	localSvc, ok := svc.(*local)
	assert.True(t, ok)
	assert.True(t, localSvc.evalCaseParallelInferenceEnabled)
	assert.NotNil(t, localSvc.evalCaseInferencePool)
	assert.Equal(t, 2, localSvc.evalCaseInferencePool.Cap())

	assert.NoError(t, svc.Close())
}

func TestLocalInferenceRequestValidation(t *testing.T) {
	ctx := context.Background()
	mgr := evalsetinmemory.New()
	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc := newLocalService(t, &fakeRunner{}, mgr, resMgr, reg, "session")

	results, err := svc.Inference(ctx, nil)
	assert.Error(t, err)
	assert.Nil(t, results)

	results, err = svc.Inference(ctx, &service.InferenceRequest{})
	assert.Error(t, err)
	assert.Nil(t, results)

	results, err = svc.Inference(ctx, &service.InferenceRequest{AppName: "app"})
	assert.Error(t, err)
	assert.Nil(t, results)
}

func TestLocalInferenceFiltersCases(t *testing.T) {
	ctx := context.Background()
	appName := "math-app"
	evalSetID := "math-set"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-add", "calc add 1 2")))
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-mul", "calc multiply 3 4")))

	runnerStub := &fakeRunner{events: []*event.Event{makeFinalEvent("calc result: 3")}}
	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc := newLocalService(t, runnerStub, mgr, resMgr, reg, "session-123")

	req := &service.InferenceRequest{
		AppName:     appName,
		EvalSetID:   evalSetID,
		EvalCaseIDs: []string{"case-add"},
	}
	results, err := svc.Inference(ctx, req)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "case-add", results[0].EvalCaseID)
	assert.Equal(t, "session-123", results[0].SessionID)
	assert.Equal(t, "demo-user", results[0].UserID)
	assert.Equal(t, status.EvalStatusPassed, results[0].Status)
	assert.Len(t, results[0].Inferences, 1)
	assert.NotNil(t, results[0].Inferences[0].FinalResponse)
	assert.Equal(t, "calc result: 3", results[0].Inferences[0].FinalResponse.Content)

	runnerStub.mu.Lock()
	callCount := len(runnerStub.calls)
	var prompt string
	if callCount > 0 {
		prompt = runnerStub.calls[0].Content
	}
	runnerStub.mu.Unlock()
	assert.Equal(t, 1, callCount)
	assert.Equal(t, "calc add 1 2", prompt)
}

func TestLocalInferenceSkipsNilEvalCases(t *testing.T) {
	ctx := context.Background()
	appName := "trace-app"
	evalSetID := "trace-set"
	baseDir := t.TempDir()

	path := filepath.Join(baseDir, appName, evalSetID+".evalset.json")
	assert.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NoError(t, os.WriteFile(path, []byte(`{
  "evalSetId": "trace-set",
  "name": "trace-set",
  "evalCases": [
    null,
    {
      "evalId": "trace-case",
      "evalMode": "trace",
      "conversation": [
        {
          "invocationId": "trace-case-1",
          "userContent": {
            "role": "user",
            "content": "hello"
          },
          "finalResponse": {
            "role": "assistant",
            "content": "world"
          }
        }
      ],
      "sessionInput": {
        "appName": "trace-app",
        "userId": "demo-user"
      }
    }
  ]
}`), 0o644))

	mgr := evalsetlocal.New(evalset.WithBaseDir(baseDir))
	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc := newLocalService(t, &fakeRunner{err: errors.New("runner should not be called")}, mgr, resMgr, reg, "session-trace")

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "trace-case", results[0].EvalCaseID)
	assert.Equal(t, evalset.EvalModeTrace, results[0].EvalMode)
	assert.Equal(t, "session-trace", results[0].SessionID)
	assert.Len(t, results[0].Inferences, 1)
	assert.NotNil(t, results[0].Inferences[0].FinalResponse)
	assert.Equal(t, "world", results[0].Inferences[0].FinalResponse.Content)
}

func TestLocalInferenceEvalSetError(t *testing.T) {
	ctx := context.Background()
	mgr := evalsetinmemory.New()
	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc := newLocalService(t, &fakeRunner{}, mgr, resMgr, reg, "session")

	req := &service.InferenceRequest{AppName: "app", EvalSetID: "missing"}
	results, err := svc.Inference(ctx, req)
	assert.Error(t, err)
	assert.Nil(t, results)
}

func TestLocalInferenceNoMatchingCases(t *testing.T) {
	ctx := context.Background()
	appName := "math-app"
	evalSetID := "math-set"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-one", "question")))

	runnerStub := &fakeRunner{events: []*event.Event{makeFinalEvent("ignored")}}
	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc := newLocalService(t, runnerStub, mgr, resMgr, reg, "session")

	req := &service.InferenceRequest{
		AppName:     appName,
		EvalSetID:   evalSetID,
		EvalCaseIDs: []string{"case-missing"},
	}
	results, err := svc.Inference(ctx, req)
	assert.NoError(t, err)
	assert.Empty(t, results)

	runnerStub.mu.Lock()
	callCount := len(runnerStub.calls)
	runnerStub.mu.Unlock()
	assert.Zero(t, callCount)
}

func TestLocalInferenceRunnerError(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case", "prompt")))

	runnerStub := &fakeRunner{err: errors.New("run failed")}
	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc := newLocalService(t, runnerStub, mgr, resMgr, reg, "session")

	req := &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID}
	results, err := svc.Inference(ctx, req)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "case", results[0].EvalCaseID)
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
	assert.Equal(t, "demo-user", results[0].UserID)
	assert.Contains(t, results[0].ErrorMessage, "run failed")
}

func TestLocalInferenceInvalidSessionInput(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	invalid := &evalset.EvalCase{
		EvalID:       "case",
		Conversation: []*evalset.Invocation{makeInvocation("inv-1", "prompt")},
		SessionInput: nil,
	}
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, invalid))

	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc, err := New(
		&fakeRunner{events: []*event.Event{makeFinalEvent("resp")}},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(resMgr),
		service.WithRegistry(reg),
		service.WithEvalCaseParallelInferenceEnabled(true),
		service.WithEvalCaseParallelism(2),
	)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "case", results[0].EvalCaseID)
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
	assert.Empty(t, results[0].UserID)
	assert.Contains(t, results[0].ErrorMessage, "session input is nil")
}

func TestLocalInferenceParallelInvokeFailureAddsContext(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case", "prompt")))

	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc, err := New(
		&fakeRunner{events: []*event.Event{makeFinalEvent("resp")}},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(resMgr),
		service.WithRegistry(reg),
		service.WithSessionIDSupplier(func(ctx context.Context) string {
			return "session-123"
		}),
		service.WithEvalCaseParallelInferenceEnabled(true),
		service.WithEvalCaseParallelism(1),
	)
	assert.NoError(t, err)
	assert.NoError(t, svc.Close())

	results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "case", results[0].EvalCaseID)
	assert.Equal(t, "session-123", results[0].SessionID)
	assert.Equal(t, "demo-user", results[0].UserID)
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
	assert.Contains(t, results[0].ErrorMessage, "submit inference task for eval case case")
	assert.Nil(t, results[0].Inferences)
}

func TestLocalInferenceParallelOrder(t *testing.T) {
	ctx := context.Background()
	appName := "math-app"
	evalSetID := "math-set"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-slow", "slow")))
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-fast", "fast")))

	runnerStub := &controlledRunner{
		started:     make(chan string, 2),
		finished:    make(chan string, 2),
		fastRelease: make(chan struct{}, 1),
		slowRelease: make(chan struct{}, 1),
	}
	defer func() {
		select {
		case runnerStub.fastRelease <- struct{}{}:
		default:
		}
		select {
		case runnerStub.slowRelease <- struct{}{}:
		default:
		}
	}()

	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc, err := New(
		runnerStub,
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(resMgr),
		service.WithRegistry(reg),
		service.WithEvalCaseParallelInferenceEnabled(true),
		service.WithEvalCaseParallelism(2),
	)
	assert.NoError(t, err)

	type outcome struct {
		results []*service.InferenceResult
		err     error
	}
	outCh := make(chan outcome, 1)
	go func() {
		results, err := svc.Inference(ctx, &service.InferenceRequest{
			AppName:   appName,
			EvalSetID: evalSetID,
		})
		outCh <- outcome{results: results, err: err}
	}()

	started := make(map[string]struct{})
	deadline := time.After(2 * time.Second)
	for len(started) < 2 {
		select {
		case msg := <-runnerStub.started:
			started[msg] = struct{}{}
		case <-deadline:
			assert.FailNow(t, "timeout waiting for runner start")
		}
	}
	_, slowStarted := started["slow"]
	_, fastStarted := started["fast"]
	assert.True(t, slowStarted)
	assert.True(t, fastStarted)
	assert.Equal(t, int32(2), atomic.LoadInt32(&runnerStub.maxConcurrent))

	runnerStub.fastRelease <- struct{}{}
	select {
	case msg := <-runnerStub.finished:
		assert.Equal(t, "fast", msg)
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for fast case completion")
	}

	runnerStub.slowRelease <- struct{}{}
	select {
	case msg := <-runnerStub.finished:
		assert.Equal(t, "slow", msg)
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for slow case completion")
	}

	var got outcome
	select {
	case got = <-outCh:
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for inference results")
	}
	assert.NoError(t, got.err)
	assert.Len(t, got.results, 2)
	assert.Equal(t, "case-slow", got.results[0].EvalCaseID)
	assert.Equal(t, "case-fast", got.results[1].EvalCaseID)
	assert.Equal(t, status.EvalStatusPassed, got.results[0].Status)
	assert.Equal(t, status.EvalStatusPassed, got.results[1].Status)
	assert.Len(t, got.results[0].Inferences, 1)
	assert.Len(t, got.results[1].Inferences, 1)
	assert.NotNil(t, got.results[0].Inferences[0].FinalResponse)
	assert.NotNil(t, got.results[1].Inferences[0].FinalResponse)
	assert.Equal(t, "resp:slow", got.results[0].Inferences[0].FinalResponse.Content)
	assert.Equal(t, "resp:fast", got.results[1].Inferences[0].FinalResponse.Content)
}

func TestLocalInferenceParallelismOneRunsSerial(t *testing.T) {
	ctx := context.Background()
	appName := "math-app"
	evalSetID := "math-set"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-slow", "slow")))
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-fast", "fast")))

	runnerStub := &controlledRunner{
		started:     make(chan string, 2),
		finished:    make(chan string, 2),
		fastRelease: make(chan struct{}, 1),
		slowRelease: make(chan struct{}, 1),
	}
	defer func() {
		select {
		case runnerStub.fastRelease <- struct{}{}:
		default:
		}
		select {
		case runnerStub.slowRelease <- struct{}{}:
		default:
		}
	}()

	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc, err := New(
		runnerStub,
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(resMgr),
		service.WithRegistry(reg),
		service.WithEvalCaseParallelInferenceEnabled(true),
		service.WithEvalCaseParallelism(1),
	)
	assert.NoError(t, err)

	type outcome struct {
		results []*service.InferenceResult
		err     error
	}
	outCh := make(chan outcome, 1)
	go func() {
		results, err := svc.Inference(ctx, &service.InferenceRequest{
			AppName:   appName,
			EvalSetID: evalSetID,
		})
		outCh <- outcome{results: results, err: err}
	}()

	select {
	case msg := <-runnerStub.started:
		assert.Equal(t, "slow", msg)
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for slow case start")
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&runnerStub.maxConcurrent))

	select {
	case msg := <-runnerStub.started:
		assert.FailNow(t, "unexpected second case start", msg)
	case <-time.After(100 * time.Millisecond):
	}

	runnerStub.slowRelease <- struct{}{}
	select {
	case msg := <-runnerStub.finished:
		assert.Equal(t, "slow", msg)
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for slow case completion")
	}

	select {
	case msg := <-runnerStub.started:
		assert.Equal(t, "fast", msg)
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for fast case start")
	}
	runnerStub.fastRelease <- struct{}{}
	select {
	case msg := <-runnerStub.finished:
		assert.Equal(t, "fast", msg)
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for fast case completion")
	}

	var got outcome
	select {
	case got = <-outCh:
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for inference results")
	}
	assert.NoError(t, got.err)
	assert.Len(t, got.results, 2)
	assert.Equal(t, "case-slow", got.results[0].EvalCaseID)
	assert.Equal(t, "case-fast", got.results[1].EvalCaseID)
}

func TestLocalEvaluateRequestValidation(t *testing.T) {
	ctx := context.Background()
	mgr := evalsetinmemory.New()
	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc := newLocalService(t, &fakeRunner{}, mgr, resMgr, reg, "session")

	result, err := svc.Evaluate(ctx, nil)
	assert.Error(t, err)
	assert.Nil(t, result)

	result, err = svc.Evaluate(ctx, &service.EvaluateRequest{})
	assert.Error(t, err)
	assert.Nil(t, result)

	result, err = svc.Evaluate(ctx, &service.EvaluateRequest{AppName: "app"})
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestLocalEvaluateSuccess(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	caseID := "calc"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, caseID, "calc add 1 2")))

	reg := registry.New()
	metricName := "custom_metric"
	fakeEval := &fakeEvaluator{
		name: metricName,
		result: &evaluator.EvaluateResult{
			OverallScore:  0.8,
			OverallStatus: status.EvalStatusPassed,
			PerInvocationResults: []*evaluator.PerInvocationResult{
				{Score: 0.8, Status: status.EvalStatusPassed},
			},
		},
	}
	assert.NoError(t, reg.Register(metricName, fakeEval))

	resMgr := evalresultinmemory.New()
	svc := newLocalService(t, &fakeRunner{}, mgr, resMgr, reg, "session-xyz")
	actual := makeActualInvocation("generated", "calc add 1 2", "calc result: 3")
	inference := makeInferenceResult(appName, evalSetID, caseID, "session-xyz", []*evalset.Invocation{actual})
	req := &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{inference},
		EvaluateConfig: &service.EvaluateConfig{
			EvalMetrics: []*metric.EvalMetric{{MetricName: metricName, Threshold: 0.5}},
		},
	}

	result, err := svc.Evaluate(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, evalSetID, result.EvalSetID)
	assert.NotEmpty(t, result.EvalSetResultID)
	assert.Len(t, result.EvalCaseResults, 1)

	caseResult := result.EvalCaseResults[0]
	assert.Equal(t, caseID, caseResult.EvalID)
	assert.Equal(t, status.EvalStatusPassed, caseResult.FinalEvalStatus)
	assert.Len(t, caseResult.OverallEvalMetricResults, 1)
	assert.Equal(t, metricName, caseResult.OverallEvalMetricResults[0].MetricName)
	assert.Equal(t, 0.8, caseResult.OverallEvalMetricResults[0].Score)
	assert.Len(t, caseResult.EvalMetricResultPerInvocation, 1)
	assert.Len(t, caseResult.EvalMetricResultPerInvocation[0].EvalMetricResults, 1)
	assert.Equal(t, "demo-user", caseResult.UserID)

	storedIDs, err := resMgr.List(ctx, appName)
	assert.NoError(t, err)
	assert.Len(t, storedIDs, 1)
	stored, err := resMgr.Get(ctx, appName, result.EvalSetResultID)
	assert.NoError(t, err)
	assert.Equal(t, result.EvalSetResultID, stored.EvalSetResultID)
}

func TestLocalEvaluateInferenceFailurePersistsErrorMessage(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	caseID := "case"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, caseID, "prompt")))

	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc := newLocalService(t, &fakeRunner{}, mgr, resMgr, reg, "session-xyz")

	inference := &service.InferenceResult{
		AppName:      appName,
		EvalSetID:    evalSetID,
		EvalCaseID:   caseID,
		SessionID:    "session-xyz",
		UserID:       "demo-user",
		Status:       status.EvalStatusFailed,
		ErrorMessage: "run failed",
	}
	req := &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{inference},
		EvaluateConfig:   &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}},
	}

	result, err := svc.Evaluate(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.EvalCaseResults, 1)
	assert.Equal(t, status.EvalStatusFailed, result.EvalCaseResults[0].FinalEvalStatus)
	assert.Equal(t, "run failed", result.EvalCaseResults[0].ErrorMessage)

	stored, err := resMgr.Get(ctx, appName, result.EvalSetResultID)
	assert.NoError(t, err)
	assert.Len(t, stored.EvalCaseResults, 1)
	assert.Equal(t, "run failed", stored.EvalCaseResults[0].ErrorMessage)
}

func TestLocalEvaluatePerCaseErrors(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	prepare := func(t *testing.T) (*local, evalset.Manager, registry.Registry) {
		mgr := evalsetinmemory.New()
		reg := registry.New()
		resMgr := evalresultinmemory.New()
		svc := newLocalService(t, &fakeRunner{}, mgr, resMgr, reg, "session")
		return svc, mgr, reg
	}

	tests := []struct {
		name      string
		expectErr bool
		setup     func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig)
	}{
		{
			name:      "nil inference result",
			expectErr: true,
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, _, _ := prepare(t)
				return svc, nil, &service.EvaluateConfig{}
			},
		},
		{
			name:      "nil evaluate config",
			expectErr: true,
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, _, _ := prepare(t)
				inference := makeInferenceResult(appName, evalSetID, "case", "session", nil)
				return svc, inference, nil
			},
		},
		{
			name:      "missing eval case",
			expectErr: true,
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, _, _ := prepare(t)
				inference := makeInferenceResult(appName, evalSetID, "missing", "session", []*evalset.Invocation{})
				config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}}
				return svc, inference, config
			},
		},
		{
			name:      "invalid eval case",
			expectErr: true,
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, _ := prepare(t)
				_, err := mgr.Create(ctx, appName, evalSetID)
				assert.NoError(t, err)
				invalid := &evalset.EvalCase{
					EvalID:       "invalid",
					Conversation: []*evalset.Invocation{},
					SessionInput: &evalset.SessionInput{AppName: appName, UserID: "demo-user", State: map[string]any{}},
				}
				assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, invalid))
				actual := makeActualInvocation("actual-1", "prompt", "answer")
				inference := makeInferenceResult(appName, evalSetID, "invalid", "session", []*evalset.Invocation{actual})
				config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}}
				return svc, inference, config
			},
		},
		{
			name:      "nil session input",
			expectErr: true,
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, _ := prepare(t)
				_, err := mgr.Create(ctx, appName, evalSetID)
				assert.NoError(t, err)
				invalid := &evalset.EvalCase{
					EvalID:       "nil-session",
					Conversation: []*evalset.Invocation{makeInvocation("expected-1", "prompt")},
					SessionInput: nil,
				}
				assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, invalid))
				actual := makeActualInvocation("actual-1", "prompt", "answer")
				inference := makeInferenceResult(appName, evalSetID, "nil-session", "session", []*evalset.Invocation{actual})
				config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}}
				return svc, inference, config
			},
		},
		{
			name:      "mismatched inference count",
			expectErr: true,
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, _ := prepare(t)
				_, err := mgr.Create(ctx, appName, evalSetID)
				assert.NoError(t, err)
				assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-mismatch", "prompt")))
				inference := makeInferenceResult(appName, evalSetID, "case-mismatch", "session", []*evalset.Invocation{})
				config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}}
				return svc, inference, config
			},
		},
		{
			name:      "missing evaluator",
			expectErr: false,
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, _ := prepare(t)
				_, err := mgr.Create(ctx, appName, evalSetID)
				assert.NoError(t, err)
				assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-evaluator", "prompt")))
				actual := makeActualInvocation("actual-1", "prompt", "answer")
				inference := makeInferenceResult(appName, evalSetID, "case-evaluator", "session", []*evalset.Invocation{actual})
				config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{{MetricName: "missing_metric", Threshold: 1}}}
				return svc, inference, config
			},
		},
		{
			name:      "per invocation mismatch",
			expectErr: true,
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, reg := prepare(t)
				_, err := mgr.Create(ctx, appName, evalSetID)
				assert.NoError(t, err)
				assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-per", "prompt")))
				metricName := "metric-per"
				fakeEval := &fakeEvaluator{
					name: metricName,
					result: &evaluator.EvaluateResult{
						OverallScore:         1,
						OverallStatus:        status.EvalStatusPassed,
						PerInvocationResults: []*evaluator.PerInvocationResult{},
					},
				}
				assert.NoError(t, reg.Register(metricName, fakeEval))
				actual := makeActualInvocation("actual-1", "prompt", "answer")
				inference := makeInferenceResult(appName, evalSetID, "case-per", "session", []*evalset.Invocation{actual})
				config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{{MetricName: metricName, Threshold: 1}}}
				return svc, inference, config
			},
		},
		{
			name:      "summarize failure",
			expectErr: true,
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, reg := prepare(t)
				_, err := mgr.Create(ctx, appName, evalSetID)
				assert.NoError(t, err)
				assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-summary", "prompt")))
				metricName := "metric-summary"
				fakeEval := &fakeEvaluator{
					name: metricName,
					result: &evaluator.EvaluateResult{
						OverallScore:         0,
						OverallStatus:        status.EvalStatusUnknown,
						PerInvocationResults: []*evaluator.PerInvocationResult{{Score: 0, Status: status.EvalStatusNotEvaluated}},
					},
				}
				assert.NoError(t, reg.Register(metricName, fakeEval))
				actual := makeActualInvocation("actual-1", "prompt", "answer")
				inference := makeInferenceResult(appName, evalSetID, "case-summary", "session", []*evalset.Invocation{actual})
				config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{{MetricName: metricName, Threshold: 1}}}
				return svc, inference, config
			},
		},
		{
			name:      "evaluator error",
			expectErr: true,
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, reg := prepare(t)
				_, err := mgr.Create(ctx, appName, evalSetID)
				assert.NoError(t, err)
				assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-eval-error", "prompt")))
				metricName := "metric-err"
				fakeEval := &fakeEvaluator{name: metricName, err: errors.New("evaluation failed")}
				assert.NoError(t, reg.Register(metricName, fakeEval))
				actual := makeActualInvocation("actual-1", "prompt", "answer")
				inference := makeInferenceResult(appName, evalSetID, "case-eval-error", "session", []*evalset.Invocation{actual})
				config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{{MetricName: metricName, Threshold: 1}}}
				return svc, inference, config
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, inference, config := tc.setup(t)
			_, err := svc.evaluatePerCase(ctx, inference, config)
			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLocalInferenceTraceModeSkipsRunner(t *testing.T) {
	ctx := context.Background()
	appName := "trace-app"
	evalSetID := "trace-set"
	caseID := "case-trace"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	traceCase := &evalset.EvalCase{
		EvalID:   caseID,
		EvalMode: evalset.EvalModeTrace,
		Conversation: []*evalset.Invocation{
			makeActualInvocation("trace-inv-1", "prompt", "answer"),
		},
		SessionInput: &evalset.SessionInput{AppName: appName, UserID: "demo-user", State: map[string]any{}},
	}
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, traceCase))

	runnerStub := &fakeRunner{err: errors.New("runner should not be called in trace mode")}
	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc := newLocalService(t, runnerStub, mgr, resMgr, reg, "session-trace")

	req := &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID}
	results, err := svc.Inference(ctx, req)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, caseID, results[0].EvalCaseID)
	assert.Equal(t, "session-trace", results[0].SessionID)
	assert.Len(t, results[0].Inferences, 1)
	assert.NotNil(t, results[0].Inferences[0].FinalResponse)
	assert.Equal(t, "answer", results[0].Inferences[0].FinalResponse.Content)

	runnerStub.mu.Lock()
	callCount := len(runnerStub.calls)
	runnerStub.mu.Unlock()
	assert.Zero(t, callCount)
}

func TestLocalEvaluateTraceModeUsesUserContentAsExpected(t *testing.T) {
	ctx := context.Background()
	appName := "trace-app"
	evalSetID := "trace-set"
	caseID := "case-trace"
	metricName := "trace_metric"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, &evalset.EvalCase{
		EvalID:   caseID,
		EvalMode: evalset.EvalModeTrace,
		Conversation: []*evalset.Invocation{
			makeActualInvocation("trace-inv-1", "prompt", "answer"),
		},
		SessionInput: &evalset.SessionInput{AppName: appName, UserID: "demo-user", State: map[string]any{}},
	}))

	reg := registry.New()
	fakeEval := &fakeEvaluator{
		name: metricName,
		result: &evaluator.EvaluateResult{
			OverallScore:  1,
			OverallStatus: status.EvalStatusPassed,
			PerInvocationResults: []*evaluator.PerInvocationResult{
				{Score: 1, Status: status.EvalStatusPassed},
			},
		},
	}
	assert.NoError(t, reg.Register(metricName, fakeEval))

	resMgr := evalresultinmemory.New()
	runnerStub := &fakeRunner{err: errors.New("runner should not be called in trace mode")}
	svc := newLocalService(t, runnerStub, mgr, resMgr, reg, "session-trace")

	inferenceResults, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, inferenceResults, 1)

	evalReq := &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: inferenceResults,
		EvaluateConfig: &service.EvaluateConfig{
			EvalMetrics: []*metric.EvalMetric{{MetricName: metricName, Threshold: 0.5}},
		},
	}
	result, err := svc.Evaluate(ctx, evalReq)
	assert.NoError(t, err)
	assert.Len(t, fakeEval.receivedActuals, 1)
	assert.Len(t, fakeEval.receivedExpecteds, 1)
	assert.NotNil(t, fakeEval.receivedExpecteds[0])
	assert.NotNil(t, fakeEval.receivedExpecteds[0].UserContent)
	assert.Equal(t, "prompt", fakeEval.receivedExpecteds[0].UserContent.Content)
	assert.Nil(t, fakeEval.receivedExpecteds[0].FinalResponse)
	assert.Nil(t, fakeEval.receivedExpecteds[0].Tools)
	assert.Len(t, result.EvalCaseResults, 1)
	assert.Len(t, result.EvalCaseResults[0].EvalMetricResultPerInvocation, 1)
	perInvocation := result.EvalCaseResults[0].EvalMetricResultPerInvocation[0]
	assert.NotNil(t, perInvocation.ActualInvocation)
	assert.NotNil(t, perInvocation.ExpectedInvocation)
	assert.Equal(t, "trace-inv-1", perInvocation.ExpectedInvocation.InvocationID)
	assert.NotNil(t, perInvocation.ExpectedInvocation.UserContent)
	assert.Equal(t, "prompt", perInvocation.ExpectedInvocation.UserContent.Content)
	assert.Nil(t, perInvocation.ExpectedInvocation.FinalResponse)
	assert.Nil(t, perInvocation.ExpectedInvocation.Tools)
	assert.Nil(t, perInvocation.ExpectedInvocation.IntermediateResponses)
}

func TestLocalInferenceParallelBeforeInferenceCaseReceivesSharedRequest(t *testing.T) {
	ctx := context.Background()
	appName := "math-app"
	evalSetID := "math-set"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-1", "calc add 1 2")))
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-2", "calc add 3 4")))

	runnerStub := &fakeRunner{events: []*event.Event{makeFinalEvent("resp")}}
	callbacks := &service.Callbacks{}
	started := make(chan *service.InferenceRequest, 2)
	release := make(chan struct{})
	callbacks.Register("probe", &service.Callback{
		BeforeInferenceCase: func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
			started <- args.Request
			<-release
			return nil, nil
		},
	})

	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc, err := New(
		runnerStub,
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(resMgr),
		service.WithRegistry(reg),
		service.WithCallbacks(callbacks),
		service.WithEvalCaseParallelInferenceEnabled(true),
		service.WithEvalCaseParallelism(2),
	)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	type outcome struct {
		results []*service.InferenceResult
		err     error
	}
	outCh := make(chan outcome, 1)
	go func() {
		results, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
		outCh <- outcome{results: results, err: err}
	}()

	var req1, req2 *service.InferenceRequest
	select {
	case req1 = <-started:
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for first BeforeInferenceCase callback")
	}
	select {
	case req2 = <-started:
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for second BeforeInferenceCase callback")
	}
	assert.NotNil(t, req1)
	assert.NotNil(t, req2)
	assert.Same(t, req1, req2)

	close(release)

	select {
	case got := <-outCh:
		assert.NoError(t, got.err)
		assert.Len(t, got.results, 2)
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for inference results")
	}
}

func TestLocalInferenceAfterInferenceSetCallbackErrorOverridesInferenceError(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "missing"
	mgr := evalsetinmemory.New()

	callbacks := &service.Callbacks{}
	callbacks.Register("probe", &service.Callback{
		AfterInferenceSet: func(ctx context.Context, args *service.AfterInferenceSetArgs) (*service.AfterInferenceSetResult, error) {
			return nil, errors.New("after inference set failed")
		},
	})

	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(resMgr),
		service.WithRegistry(reg),
		service.WithCallbacks(callbacks),
	)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	_, err = svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "after inference set failed")
	assert.NotContains(t, err.Error(), "load inference eval cases")
}

func TestEvalCaseInferencePoolHandlesNilEvalCase(t *testing.T) {
	pool, err := createEvalCaseInferencePool(1)
	assert.NoError(t, err)
	defer pool.Release()

	results := make([]*service.InferenceResult, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	param := &evalCaseInferenceParam{
		idx:      0,
		ctx:      context.Background(),
		req:      &service.InferenceRequest{AppName: "app", EvalSetID: "set"},
		svc:      &local{sessionIDSupplier: func(ctx context.Context) string { return "session" }},
		results:  results,
		wg:       &wg,
		evalCase: nil,
	}
	assert.NoError(t, pool.Invoke(param))
	wg.Wait()

	assert.NotNil(t, results[0])
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
	assert.Contains(t, results[0].ErrorMessage, "eval case is nil")
}

func TestLocalEvaluateAfterEvaluateSetCallbackErrorOverridesOriginalError(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	mgr := evalsetinmemory.New()

	callbacks := &service.Callbacks{}
	callbacks.Register("probe", &service.Callback{
		AfterEvaluateSet: func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
			return nil, errors.New("after evaluate set failed")
		},
	})

	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(resMgr),
		service.WithRegistry(reg),
		service.WithCallbacks(callbacks),
	)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{nil},
		EvaluateConfig:   &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "after evaluate set failed")
	assert.NotContains(t, err.Error(), "inference result is nil")
}

func TestLocalEvaluateAfterEvaluateCaseIncludesInferenceResult(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	caseID := "calc"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, caseID, "prompt")))

	callbacks := &service.Callbacks{}
	var got *service.InferenceResult
	callbacks.Register("probe", &service.Callback{
		AfterEvaluateCase: func(ctx context.Context, args *service.AfterEvaluateCaseArgs) (*service.AfterEvaluateCaseResult, error) {
			got = args.InferenceResult
			return nil, nil
		},
	})

	reg := registry.New()
	resMgr := evalresultinmemory.New()
	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithEvalResultManager(resMgr),
		service.WithRegistry(reg),
		service.WithCallbacks(callbacks),
	)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	inference := makeInferenceResult(appName, evalSetID, caseID, "session", []*evalset.Invocation{makeActualInvocation("generated", "prompt", "answer")})
	req := &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{inference},
		EvaluateConfig:   &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}},
	}
	_, err = svc.Evaluate(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, inference, got)
}
