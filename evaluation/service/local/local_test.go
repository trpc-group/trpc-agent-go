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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	llmtemplateevaluator "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/template"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	criteriontext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	metricregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/usersimulation"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type fakeRunner struct {
	events []*event.Event
	err    error

	mu         sync.Mutex
	calls      []model.Message
	sessionIDs []string
}

func (f *fakeRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	f.calls = append(f.calls, message)
	f.sessionIDs = append(f.sessionIDs, sessionID)
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

type blockingEvaluator struct {
	name          string
	result        *evaluator.EvaluateResult
	started       chan string
	release       chan struct{}
	running       int32
	maxConcurrent int32
}

func newBlockingEvaluator(name string, expectedCalls int) *blockingEvaluator {
	return &blockingEvaluator{
		name:    name,
		result:  &evaluator.EvaluateResult{OverallScore: 1, OverallStatus: status.EvalStatusPassed, PerInvocationResults: []*evaluator.PerInvocationResult{{Score: 1, Status: status.EvalStatusPassed}}},
		started: make(chan string, expectedCalls),
		release: make(chan struct{}),
	}
}

func (b *blockingEvaluator) Name() string {
	return b.name
}

func (b *blockingEvaluator) Description() string {
	return "blocking evaluator"
}

func (b *blockingEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	cur := atomic.AddInt32(&b.running, 1)
	for {
		prev := atomic.LoadInt32(&b.maxConcurrent)
		if cur <= prev {
			break
		}
		if atomic.CompareAndSwapInt32(&b.maxConcurrent, prev, cur) {
			break
		}
	}
	invocationID := ""
	if len(actuals) > 0 && actuals[0] != nil {
		invocationID = actuals[0].InvocationID
	}
	b.started <- invocationID
	<-b.release
	atomic.AddInt32(&b.running, -1)
	return b.result, nil
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

func TestLocalEvaluateResolvesMetricRegistryCompareName(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	evalCaseID := "case-1"
	evalSetManager := evalsetinmemory.New()
	_, err := evalSetManager.Create(ctx, appName, evalSetID)
	if !assert.NoError(t, err) {
		return
	}
	expectedCase := makeEvalCase(appName, evalCaseID, "question")
	expectedCase.Conversation[0].FinalResponse = &model.Message{
		Role:    model.RoleAssistant,
		Content: " answer ",
	}
	err = evalSetManager.AddCase(ctx, appName, evalSetID, expectedCase)
	if !assert.NoError(t, err) {
		return
	}
	metricDir := t.TempDir()
	metricManager := metriclocal.New(metric.WithBaseDir(metricDir))
	err = metricManager.Add(ctx, appName, evalSetID, &metric.EvalMetric{
		MetricName: "final_response_avg_score",
		Threshold:  1.0,
		Criterion: &criterion.Criterion{
			FinalResponse: &finalresponse.FinalResponseCriterion{
				Text: &criteriontext.TextCriterion{CompareName: "trim_equal"},
			},
		},
	})
	if !assert.NoError(t, err) {
		return
	}
	loadedMetric, err := metricManager.Get(ctx, appName, evalSetID, "final_response_avg_score")
	if !assert.NoError(t, err) {
		return
	}
	customMetricRegistry := metricregistry.New()
	err = customMetricRegistry.RegisterTextCompare("trim_equal", func(actual, expected string) (bool, error) {
		return strings.TrimSpace(actual) == strings.TrimSpace(expected), nil
	})
	if !assert.NoError(t, err) {
		return
	}
	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(evalSetManager),
		service.WithRegistry(registry.New()),
		service.WithMetricRegistry(customMetricRegistry),
	)
	if !assert.NoError(t, err) {
		return
	}
	result, err := svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:   appName,
		EvalSetID: evalSetID,
		InferenceResults: []*service.InferenceResult{
			{
				AppName:    appName,
				EvalSetID:  evalSetID,
				EvalCaseID: evalCaseID,
				SessionID:  "session",
				UserID:     "user",
				Status:     status.EvalStatusPassed,
				Inferences: []*evalset.Invocation{
					makeActualInvocation(evalCaseID+"-1", "question", "answer"),
				},
			},
		},
		EvaluateConfig: &service.EvaluateConfig{
			EvalMetrics: []*metric.EvalMetric{loadedMetric},
		},
	})
	if !assert.NoError(t, err) {
		return
	}
	if !assert.Len(t, result.EvalCaseResults, 1) {
		return
	}
	caseResult := result.EvalCaseResults[0]
	assert.Equal(t, status.EvalStatusPassed, caseResult.FinalEvalStatus)
	if assert.Len(t, caseResult.OverallEvalMetricResults, 1) {
		assert.Equal(t, status.EvalStatusPassed, caseResult.OverallEvalMetricResults[0].EvalStatus)
	}
	if assert.NotNil(t, loadedMetric.Criterion) && assert.NotNil(t, loadedMetric.Criterion.FinalResponse) &&
		assert.NotNil(t, loadedMetric.Criterion.FinalResponse.Text) {
		assert.NotNil(t, loadedMetric.Criterion.FinalResponse.Text.Compare)
	}
}

func TestLocalResolveMetricExtensionsValidation(t *testing.T) {
	svc := &local{}
	assert.ErrorContains(t, svc.resolveMetricExtensions(nil, metricregistry.New()), "evaluate config is nil")
	assert.ErrorContains(t, svc.resolveMetricExtensions(&service.EvaluateConfig{}, nil), "metric registry is nil")
}

func TestLocalResolveMetricExtensionsReturnsResolveError(t *testing.T) {
	svc := &local{}
	err := svc.resolveMetricExtensions(
		&service.EvaluateConfig{
			EvalMetrics: []*metric.EvalMetric{
				{
					Criterion: &criterion.Criterion{
						FinalResponse: &finalresponse.FinalResponseCriterion{
							Text: &criteriontext.TextCriterion{CompareName: "missing"},
						},
					},
				},
			},
		},
		metricregistry.New(),
	)
	assert.ErrorContains(t, err, "resolve metric at index 0")
	assert.ErrorContains(t, err, "text compare missing not found")
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

func newLocalService(t *testing.T, r runner.Runner, evalSetMgr evalset.Manager, reg registry.Registry, sessionID string) *local {
	t.Helper()
	svc, err := New(
		r,
		service.WithEvalSetManager(evalSetMgr),
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
			name: "parallel_evaluation_requires_positive_parallelism",
			r:    &fakeRunner{},
			options: []service.Option{
				service.WithEvalCaseParallelEvaluationEnabled(true),
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
	assert.False(t, localSvc.evalCaseParallelEvaluationEnabled)
	assert.Nil(t, localSvc.evalCaseInferencePools)
	assert.Nil(t, localSvc.evalCaseEvaluationPools)

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
	assert.NotNil(t, localSvc.evalCaseInferencePools)
	pool := localSvc.evalCaseInferencePools[2]
	assert.NotNil(t, pool)
	if pool == nil {
		return
	}
	assert.Equal(t, 2, pool.Cap())

	assert.NoError(t, svc.Close())
}

func TestLocalNewParallelEvaluationCreatesPool(t *testing.T) {
	svc, err := New(
		&fakeRunner{},
		service.WithEvalCaseParallelEvaluationEnabled(true),
		service.WithEvalCaseParallelism(2),
	)
	assert.NoError(t, err)
	assert.NotNil(t, svc)

	localSvc, ok := svc.(*local)
	assert.True(t, ok)
	assert.True(t, localSvc.evalCaseParallelEvaluationEnabled)
	assert.NotNil(t, localSvc.evalCaseEvaluationPools)
	pool := localSvc.evalCaseEvaluationPools[2]
	assert.NotNil(t, pool)
	if pool == nil {
		return
	}
	assert.Equal(t, 2, pool.Cap())

	assert.NoError(t, svc.Close())
}

func TestLocalInferenceRequestValidation(t *testing.T) {
	ctx := context.Background()
	mgr := evalsetinmemory.New()
	reg := registry.New()
	svc := newLocalService(t, &fakeRunner{}, mgr, reg, "session")

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
	svc := newLocalService(t, runnerStub, mgr, reg, "session-123")

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
	svc := newLocalService(t, &fakeRunner{err: errors.New("runner should not be called")}, mgr, reg, "session-trace")

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
	svc := newLocalService(t, &fakeRunner{}, mgr, reg, "session")

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
	svc := newLocalService(t, runnerStub, mgr, reg, "session")

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
	svc := newLocalService(t, runnerStub, mgr, reg, "session")

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
	svc, err := New(
		&fakeRunner{events: []*event.Event{makeFinalEvent("resp")}},
		service.WithEvalSetManager(mgr),
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
	svc, err := New(
		&fakeRunner{events: []*event.Event{makeFinalEvent("resp")}},
		service.WithEvalSetManager(mgr),
		service.WithRegistry(reg),
		service.WithSessionIDSupplier(func(ctx context.Context) string {
			return "session-123"
		}),
		service.WithEvalCaseParallelInferenceEnabled(true),
		service.WithEvalCaseParallelism(1),
	)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, svc.Close())
	}()

	localSvc, ok := svc.(*local)
	assert.True(t, ok)
	if !ok {
		return
	}
	localSvc.evalCaseInferencePoolsMu.Lock()
	pool := localSvc.evalCaseInferencePools[1]
	localSvc.evalCaseInferencePoolsMu.Unlock()
	assert.NotNil(t, pool)
	if pool == nil {
		return
	}
	pool.Release()

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
	svc, err := New(
		runnerStub,
		service.WithEvalSetManager(mgr),
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

func TestLocalInferencePerCallParallelInferenceEnabledRunsInParallel(t *testing.T) {
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
	svc, err := New(
		runnerStub,
		service.WithEvalSetManager(mgr),
		service.WithRegistry(reg),
		service.WithEvalCaseParallelism(2),
	)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	if svc == nil {
		return
	}
	defer func() { assert.NoError(t, svc.Close()) }()

	type outcome struct {
		results []*service.InferenceResult
		err     error
	}
	outCh := make(chan outcome, 1)
	go func() {
		results, err := svc.Inference(ctx, &service.InferenceRequest{
			AppName:   appName,
			EvalSetID: evalSetID,
		}, service.WithEvalCaseParallelInferenceEnabled(true))
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
	svc, err := New(
		runnerStub,
		service.WithEvalSetManager(mgr),
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

func TestLocalInferencePerCallParallelismCanChange(t *testing.T) {
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
	svc, err := New(
		runnerStub,
		service.WithEvalSetManager(mgr),
		service.WithRegistry(reg),
		service.WithEvalCaseParallelism(1),
	)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	if svc == nil {
		return
	}
	defer func() { assert.NoError(t, svc.Close()) }()

	type outcome struct {
		results []*service.InferenceResult
		err     error
	}
	outCh := make(chan outcome, 1)
	go func() {
		results, err := svc.Inference(ctx, &service.InferenceRequest{
			AppName:   appName,
			EvalSetID: evalSetID,
		},
			service.WithEvalCaseParallelInferenceEnabled(true),
			service.WithEvalCaseParallelism(2),
		)
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
}

func TestLocalEvaluateRequestValidation(t *testing.T) {
	ctx := context.Background()
	mgr := evalsetinmemory.New()
	reg := registry.New()
	svc := newLocalService(t, &fakeRunner{}, mgr, reg, "session")

	result, err := svc.Evaluate(ctx, nil)
	assert.Error(t, err)
	assert.Nil(t, result)

	result, err = svc.Evaluate(ctx, &service.EvaluateRequest{})
	assert.Error(t, err)
	assert.Nil(t, result)

	result, err = svc.Evaluate(ctx, &service.EvaluateRequest{AppName: "app"})
	assert.Error(t, err)
	assert.Nil(t, result)

	result, err = svc.Evaluate(ctx, &service.EvaluateRequest{AppName: "app", EvalSetID: "set"})
	assert.Error(t, err)
	assert.Nil(t, result)

	result, err = svc.Evaluate(
		ctx,
		&service.EvaluateRequest{
			AppName:          "app",
			EvalSetID:        "set",
			InferenceResults: []*service.InferenceResult{},
			EvaluateConfig:   &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}},
		},
		service.WithEvalSetManager(nil),
	)
	assert.Error(t, err)
	assert.Nil(t, result)

	result, err = svc.Evaluate(
		ctx,
		&service.EvaluateRequest{
			AppName:          "app",
			EvalSetID:        "set",
			InferenceResults: []*service.InferenceResult{},
			EvaluateConfig:   &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}},
		},
		service.WithRegistry(nil),
	)
	assert.Error(t, err)
	assert.Nil(t, result)

	result, err = svc.Evaluate(
		ctx,
		&service.EvaluateRequest{
			AppName:          "app",
			EvalSetID:        "set",
			InferenceResults: []*service.InferenceResult{},
			EvaluateConfig:   &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}},
		},
		service.WithEvalCaseParallelEvaluationEnabled(true),
		service.WithEvalCaseParallelism(0),
	)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestLocalEvaluateParallelInvokeFailureAddsContext(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	reg := registry.New()
	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithRegistry(reg),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session-123" }),
		service.WithEvalCaseParallelEvaluationEnabled(true),
		service.WithEvalCaseParallelism(1),
	)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	defer func() { assert.NoError(t, svc.Close()) }()

	localSvc, ok := svc.(*local)
	assert.True(t, ok)
	if !ok {
		return
	}
	localSvc.evalCaseEvaluationPoolsMu.Lock()
	pool := localSvc.evalCaseEvaluationPools[1]
	localSvc.evalCaseEvaluationPoolsMu.Unlock()
	assert.NotNil(t, pool)
	if pool == nil {
		return
	}
	pool.Release()

	req := &service.EvaluateRequest{
		AppName:   appName,
		EvalSetID: evalSetID,
		InferenceResults: []*service.InferenceResult{
			{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case", Status: status.EvalStatusPassed},
		},
		EvaluateConfig: &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}},
	}
	result, err := svc.Evaluate(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, result)
	if err != nil {
		assert.Contains(t, err.Error(), "submit evaluation task for eval case case")
	}
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

	svc := newLocalService(t, &fakeRunner{}, mgr, reg, "session-xyz")
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
	assert.Equal(t, appName, result.AppName)
	assert.Equal(t, evalSetID, result.EvalSetID)
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
}

func TestLocalEvaluateParallelEvaluationPreservesOrder(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-1", "prompt-1")))
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-2", "prompt-2")))

	reg := registry.New()
	metricName := "blocking_metric"
	blocking := newBlockingEvaluator(metricName, 2)
	assert.NoError(t, reg.Register(metricName, blocking))

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithRegistry(reg),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session-xyz" }),
		service.WithEvalCaseParallelEvaluationEnabled(true),
		service.WithEvalCaseParallelism(2),
	)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	if svc == nil {
		return
	}
	defer func() { assert.NoError(t, svc.Close()) }()

	inference1 := makeInferenceResult(appName, evalSetID, "case-1", "session-xyz", []*evalset.Invocation{
		makeActualInvocation("case-1-actual", "prompt-1", "resp-1"),
	})
	inference2 := makeInferenceResult(appName, evalSetID, "case-2", "session-xyz", []*evalset.Invocation{
		makeActualInvocation("case-2-actual", "prompt-2", "resp-2"),
	})
	req := &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{inference1, inference2},
		EvaluateConfig: &service.EvaluateConfig{
			EvalMetrics: []*metric.EvalMetric{{MetricName: metricName, Threshold: 0.5}},
		},
	}

	type response struct {
		result *service.EvalSetRunResult
		err    error
	}
	respCh := make(chan response, 1)
	go func() {
		result, err := svc.Evaluate(ctx, req)
		respCh <- response{result: result, err: err}
	}()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-blocking.started:
		case <-deadline.C:
			assert.FailNow(t, "timeout waiting for evaluator calls")
		}
	}
	close(blocking.release)

	resp := <-respCh
	assert.NoError(t, resp.err)
	assert.NotNil(t, resp.result)
	if resp.result == nil {
		return
	}
	assert.Len(t, resp.result.EvalCaseResults, 2)
	assert.Equal(t, "case-1", resp.result.EvalCaseResults[0].EvalID)
	assert.Equal(t, "case-2", resp.result.EvalCaseResults[1].EvalID)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&blocking.maxConcurrent), int32(2))
}

func TestLocalEvaluatePerCallParallelEvaluationEnabledPreservesOrder(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-1", "prompt-1")))
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-2", "prompt-2")))

	reg := registry.New()
	metricName := "blocking_metric"
	blocking := newBlockingEvaluator(metricName, 2)
	assert.NoError(t, reg.Register(metricName, blocking))

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithRegistry(reg),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session-xyz" }),
		service.WithEvalCaseParallelism(2),
	)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	if svc == nil {
		return
	}
	defer func() { assert.NoError(t, svc.Close()) }()

	inference1 := makeInferenceResult(appName, evalSetID, "case-1", "session-xyz", []*evalset.Invocation{
		makeActualInvocation("case-1-actual", "prompt-1", "resp-1"),
	})
	inference2 := makeInferenceResult(appName, evalSetID, "case-2", "session-xyz", []*evalset.Invocation{
		makeActualInvocation("case-2-actual", "prompt-2", "resp-2"),
	})
	req := &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{inference1, inference2},
		EvaluateConfig: &service.EvaluateConfig{
			EvalMetrics: []*metric.EvalMetric{{MetricName: metricName, Threshold: 0.5}},
		},
	}

	type response struct {
		result *service.EvalSetRunResult
		err    error
	}
	respCh := make(chan response, 1)
	go func() {
		result, err := svc.Evaluate(ctx, req, service.WithEvalCaseParallelEvaluationEnabled(true))
		respCh <- response{result: result, err: err}
	}()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-blocking.started:
		case <-deadline.C:
			assert.FailNow(t, "timeout waiting for evaluator calls")
		}
	}
	close(blocking.release)

	resp := <-respCh
	assert.NoError(t, resp.err)
	assert.NotNil(t, resp.result)
	if resp.result == nil {
		return
	}
	assert.Len(t, resp.result.EvalCaseResults, 2)
	assert.Equal(t, "case-1", resp.result.EvalCaseResults[0].EvalID)
	assert.Equal(t, "case-2", resp.result.EvalCaseResults[1].EvalID)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&blocking.maxConcurrent), int32(2))
}

func TestLocalEvaluatePerCallParallelismCanChange(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-1", "prompt-1")))
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-2", "prompt-2")))

	reg := registry.New()
	metricName := "blocking_metric"
	blocking := newBlockingEvaluator(metricName, 2)
	assert.NoError(t, reg.Register(metricName, blocking))

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithRegistry(reg),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session-xyz" }),
		service.WithEvalCaseParallelism(1),
	)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	if svc == nil {
		return
	}
	defer func() { assert.NoError(t, svc.Close()) }()

	inference1 := makeInferenceResult(appName, evalSetID, "case-1", "session-xyz", []*evalset.Invocation{
		makeActualInvocation("case-1-actual", "prompt-1", "resp-1"),
	})
	inference2 := makeInferenceResult(appName, evalSetID, "case-2", "session-xyz", []*evalset.Invocation{
		makeActualInvocation("case-2-actual", "prompt-2", "resp-2"),
	})
	req := &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{inference1, inference2},
		EvaluateConfig: &service.EvaluateConfig{
			EvalMetrics: []*metric.EvalMetric{{MetricName: metricName, Threshold: 0.5}},
		},
	}

	type response struct {
		result *service.EvalSetRunResult
		err    error
	}
	respCh := make(chan response, 1)
	go func() {
		result, err := svc.Evaluate(ctx, req,
			service.WithEvalCaseParallelEvaluationEnabled(true),
			service.WithEvalCaseParallelism(2),
		)
		respCh <- response{result: result, err: err}
	}()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-blocking.started:
		case <-deadline.C:
			assert.FailNow(t, "timeout waiting for evaluator calls")
		}
	}
	close(blocking.release)

	resp := <-respCh
	assert.NoError(t, resp.err)
	assert.NotNil(t, resp.result)
	if resp.result == nil {
		return
	}
	assert.Len(t, resp.result.EvalCaseResults, 2)
	assert.Equal(t, "case-1", resp.result.EvalCaseResults[0].EvalID)
	assert.Equal(t, "case-2", resp.result.EvalCaseResults[1].EvalID)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&blocking.maxConcurrent), int32(2))
}

func TestLocalEvaluateParallelEvaluationAggregatesErrors(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	started := make(chan struct{}, 1)
	release := make(chan struct{})

	callbacks := &service.Callbacks{}
	callbacks.Register("fail", &service.Callback{
		BeforeEvaluateCase: func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
			if args.EvalCaseID == "case-2" {
				select {
				case started <- struct{}{}:
				default:
				}
				select {
				case <-release:
					return nil, errors.New("case-2 failed")
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, errors.New("case-1 failed")
		},
	})

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(evalsetinmemory.New()),
		service.WithEvalResultManager(evalresultinmemory.New()),
		service.WithRegistry(registry.New()),
		service.WithCallbacks(callbacks),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
		service.WithEvalCaseParallelEvaluationEnabled(true),
		service.WithEvalCaseParallelism(2),
	)
	assert.NoError(t, err)
	if err != nil {
		return
	}
	if svc == nil {
		return
	}
	defer func() { assert.NoError(t, svc.Close()) }()

	req := &service.EvaluateRequest{
		AppName:   appName,
		EvalSetID: evalSetID,
		InferenceResults: []*service.InferenceResult{
			{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-1", SessionID: "session", Status: status.EvalStatusFailed, ErrorMessage: "failed"},
			{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-2", SessionID: "session", Status: status.EvalStatusFailed, ErrorMessage: "failed"},
		},
		EvaluateConfig: &service.EvaluateConfig{},
	}

	done := make(chan error, 1)
	go func() {
		_, err := svc.Evaluate(ctx, req)
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for case-2 callback")
	}

	close(release)

	select {
	case err := <-done:
		assert.Error(t, err)
		if err == nil {
			return
		}
		assert.Contains(t, err.Error(), "evalCaseID=case-1")
		assert.Contains(t, err.Error(), "case-1 failed")
		assert.Contains(t, err.Error(), "evalCaseID=case-2")
		assert.Contains(t, err.Error(), "case-2 failed")
		assert.NotContains(t, err.Error(), "context canceled")
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for evaluation completion")
	}
}

func TestLocalEvaluateInferenceFailureReturnsErrorMessage(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	caseID := "case"
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, caseID, "prompt")))

	reg := registry.New()
	svc := newLocalService(t, &fakeRunner{}, mgr, reg, "session-xyz")

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
}

func TestLocalEvaluatePerCaseErrors(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"

	prepare := func(t *testing.T) (*local, evalset.Manager, registry.Registry) {
		mgr := evalsetinmemory.New()
		reg := registry.New()
		svc := newLocalService(t, &fakeRunner{}, mgr, reg, "session")
		return svc, mgr, reg
	}

	tests := []struct {
		name      string
		expectErr bool
		setup     func(t *testing.T) (*local, evalset.Manager, registry.Registry, *service.InferenceResult, *service.EvaluateConfig)
	}{
		{
			name:      "nil inference result",
			expectErr: true,
			setup: func(t *testing.T) (*local, evalset.Manager, registry.Registry, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, reg := prepare(t)
				return svc, mgr, reg, nil, &service.EvaluateConfig{}
			},
		},
		{
			name:      "nil evaluate config",
			expectErr: true,
			setup: func(t *testing.T) (*local, evalset.Manager, registry.Registry, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, reg := prepare(t)
				inference := makeInferenceResult(appName, evalSetID, "case", "session", nil)
				return svc, mgr, reg, inference, nil
			},
		},
		{
			name:      "missing eval case",
			expectErr: true,
			setup: func(t *testing.T) (*local, evalset.Manager, registry.Registry, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, reg := prepare(t)
				inference := makeInferenceResult(appName, evalSetID, "missing", "session", []*evalset.Invocation{})
				config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}}
				return svc, mgr, reg, inference, config
			},
		},
		{
			name:      "invalid eval case",
			expectErr: true,
			setup: func(t *testing.T) (*local, evalset.Manager, registry.Registry, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, reg := prepare(t)
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
				return svc, mgr, reg, inference, config
			},
		},
		{
			name:      "nil session input",
			expectErr: true,
			setup: func(t *testing.T) (*local, evalset.Manager, registry.Registry, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, reg := prepare(t)
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
				return svc, mgr, reg, inference, config
			},
		},
		{
			name:      "mismatched inference count",
			expectErr: true,
			setup: func(t *testing.T) (*local, evalset.Manager, registry.Registry, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, reg := prepare(t)
				_, err := mgr.Create(ctx, appName, evalSetID)
				assert.NoError(t, err)
				assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-mismatch", "prompt")))
				inference := makeInferenceResult(appName, evalSetID, "case-mismatch", "session", []*evalset.Invocation{})
				config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}}
				return svc, mgr, reg, inference, config
			},
		},
		{
			name:      "missing evaluator",
			expectErr: false,
			setup: func(t *testing.T) (*local, evalset.Manager, registry.Registry, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, reg := prepare(t)
				_, err := mgr.Create(ctx, appName, evalSetID)
				assert.NoError(t, err)
				assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, makeEvalCase(appName, "case-evaluator", "prompt")))
				actual := makeActualInvocation("actual-1", "prompt", "answer")
				inference := makeInferenceResult(appName, evalSetID, "case-evaluator", "session", []*evalset.Invocation{actual})
				config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{{MetricName: "missing_metric", Threshold: 1}}}
				return svc, mgr, reg, inference, config
			},
		},
		{
			name:      "per invocation mismatch",
			expectErr: true,
			setup: func(t *testing.T) (*local, evalset.Manager, registry.Registry, *service.InferenceResult, *service.EvaluateConfig) {
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
				return svc, mgr, reg, inference, config
			},
		},
		{
			name:      "summarize failure",
			expectErr: true,
			setup: func(t *testing.T) (*local, evalset.Manager, registry.Registry, *service.InferenceResult, *service.EvaluateConfig) {
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
				return svc, mgr, reg, inference, config
			},
		},
		{
			name:      "evaluator error",
			expectErr: true,
			setup: func(t *testing.T) (*local, evalset.Manager, registry.Registry, *service.InferenceResult, *service.EvaluateConfig) {
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
				return svc, mgr, reg, inference, config
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, mgr, reg, inference, config := tc.setup(t)
			opts := &service.Options{EvalSetManager: mgr, Registry: reg}
			_, err := svc.evaluatePerCase(ctx, inference, config, opts)
			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLocalEvaluatePerCaseRejectsNilManagers(t *testing.T) {
	ctx := context.Background()

	svc := &local{}
	inference := &service.InferenceResult{EvalCaseID: "case"}
	config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}}

	_, err := svc.evaluatePerCase(ctx, inference, config, &service.Options{
		EvalSetManager: nil,
		Registry:       registry.New(),
	})
	assert.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "eval set manager is nil")
	}

	_, err = svc.evaluatePerCase(ctx, inference, config, &service.Options{
		EvalSetManager: evalsetinmemory.New(),
		Registry:       nil,
	})
	assert.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "registry is nil")
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
	svc := newLocalService(t, runnerStub, mgr, reg, "session-trace")

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

func TestLocalInferenceDefaultModeRejectsActualConversation(t *testing.T) {
	ctx := context.Background()
	appName := "fixture-app"
	evalSetID := "fixture-set"
	caseID := "case-fixture"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)

	fixtureCase := &evalset.EvalCase{
		EvalID: caseID,
		Conversation: []*evalset.Invocation{
			makeInvocation("fixture-inv-1", "prompt"),
		},
		ActualConversation: []*evalset.Invocation{
			&evalset.Invocation{
				FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "answer"},
			},
		},
		SessionInput: &evalset.SessionInput{AppName: appName, UserID: "demo-user", State: map[string]any{}},
	}
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, fixtureCase))

	runnerStub := &fakeRunner{err: errors.New("runner should not be called when actualConversation is set")}
	reg := registry.New()
	svc := newLocalService(t, runnerStub, mgr, reg, "session-fixture")

	req := &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID}
	results, err := svc.Inference(ctx, req)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, caseID, results[0].EvalCaseID)
	assert.Equal(t, "session-fixture", results[0].SessionID)
	assert.Equal(t, status.EvalStatusFailed, results[0].Status)
	assert.Contains(t, results[0].ErrorMessage, "actualConversation is only supported in trace mode")

	runnerStub.mu.Lock()
	callCount := len(runnerStub.calls)
	runnerStub.mu.Unlock()
	assert.Zero(t, callCount)
}

func TestLocalInferenceTraceModeUsesConfiguredActualConversation(t *testing.T) {
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
			makeInvocation("trace-inv-1", "prompt"),
		},
		ActualConversation: []*evalset.Invocation{
			makeActualInvocation("trace-inv-1", "prompt", "answer"),
		},
		SessionInput: &evalset.SessionInput{AppName: appName, UserID: "demo-user", State: map[string]any{}},
	}
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, traceCase))

	runnerStub := &fakeRunner{err: errors.New("runner should not be called when actualConversation is set")}
	reg := registry.New()
	svc := newLocalService(t, runnerStub, mgr, reg, "session-trace")

	req := &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID}
	results, err := svc.Inference(ctx, req)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, caseID, results[0].EvalCaseID)
	assert.Equal(t, "session-trace", results[0].SessionID)
	assert.Len(t, results[0].Inferences, 1)
	assert.Equal(t, "trace-inv-1", results[0].Inferences[0].InvocationID)
	assert.NotNil(t, results[0].Inferences[0].FinalResponse)
	assert.Equal(t, "answer", results[0].Inferences[0].FinalResponse.Content)

	runnerStub.mu.Lock()
	callCount := len(runnerStub.calls)
	runnerStub.mu.Unlock()
	assert.Zero(t, callCount)
}

func TestLocalInferenceTraceModeAllowsActualConversationWithoutConversation(t *testing.T) {
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
		ActualConversation: []*evalset.Invocation{
			makeActualInvocation("trace-inv-1", "prompt", "answer"),
		},
		SessionInput: &evalset.SessionInput{AppName: appName, UserID: "demo-user", State: map[string]any{}},
	}
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, traceCase))

	runnerStub := &fakeRunner{err: errors.New("runner should not be called in trace mode")}
	reg := registry.New()
	svc := newLocalService(t, runnerStub, mgr, reg, "session-trace")

	req := &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID}
	results, err := svc.Inference(ctx, req)
	assert.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, caseID, results[0].EvalCaseID)
	assert.Equal(t, status.EvalStatusPassed, results[0].Status)
	assert.Len(t, results[0].Inferences, 1)
	assert.Equal(t, "trace-inv-1", results[0].Inferences[0].InvocationID)
	assert.NotNil(t, results[0].Inferences[0].UserContent)
	assert.Equal(t, "prompt", results[0].Inferences[0].UserContent.Content)
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

	runnerStub := &fakeRunner{err: errors.New("runner should not be called in trace mode")}
	svc := newLocalService(t, runnerStub, mgr, reg, "session-trace")

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

func TestLocalEvaluateTraceModeUsesUserContentAsExpectedWhenConversationIsOmitted(t *testing.T) {
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
		ActualConversation: []*evalset.Invocation{
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

	runnerStub := &fakeRunner{err: errors.New("runner should not be called in trace mode")}
	svc := newLocalService(t, runnerStub, mgr, reg, "session-trace")

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
	assert.NotNil(t, fakeEval.receivedActuals[0])
	assert.NotNil(t, fakeEval.receivedActuals[0].FinalResponse)
	assert.Equal(t, "answer", fakeEval.receivedActuals[0].FinalResponse.Content)
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

func TestLocalEvaluateTraceModeUsesConversationAsExpectedWhenActualConversationIsConfigured(t *testing.T) {
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
			makeActualInvocation("trace-inv-1", "prompt", "expected"),
		},
		ActualConversation: []*evalset.Invocation{
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

	runnerStub := &fakeRunner{err: errors.New("runner should not be called in trace mode")}
	svc := newLocalService(t, runnerStub, mgr, reg, "session-trace")

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
	assert.NotNil(t, fakeEval.receivedActuals[0])
	assert.Equal(t, "trace-inv-1", fakeEval.receivedActuals[0].InvocationID)
	assert.NotNil(t, fakeEval.receivedActuals[0].UserContent)
	assert.Equal(t, "prompt", fakeEval.receivedActuals[0].UserContent.Content)
	assert.NotNil(t, fakeEval.receivedActuals[0].FinalResponse)
	assert.Equal(t, "answer", fakeEval.receivedActuals[0].FinalResponse.Content)
	assert.NotNil(t, fakeEval.receivedExpecteds[0])
	assert.Equal(t, "trace-inv-1", fakeEval.receivedExpecteds[0].InvocationID)
	assert.NotNil(t, fakeEval.receivedExpecteds[0].UserContent)
	assert.Equal(t, "prompt", fakeEval.receivedExpecteds[0].UserContent.Content)
	assert.NotNil(t, fakeEval.receivedExpecteds[0].FinalResponse)
	assert.Equal(t, "expected", fakeEval.receivedExpecteds[0].FinalResponse.Content)

	assert.Len(t, result.EvalCaseResults, 1)
	assert.Len(t, result.EvalCaseResults[0].EvalMetricResultPerInvocation, 1)
	perInvocation := result.EvalCaseResults[0].EvalMetricResultPerInvocation[0]
	assert.Equal(t, "trace-inv-1", perInvocation.ExpectedInvocation.InvocationID)
	assert.NotNil(t, perInvocation.ExpectedInvocation.UserContent)
	assert.Equal(t, "prompt", perInvocation.ExpectedInvocation.UserContent.Content)
	assert.NotNil(t, perInvocation.ExpectedInvocation.FinalResponse)
	assert.Equal(t, "expected", perInvocation.ExpectedInvocation.FinalResponse.Content)
}

func TestLocalEvaluateExpectedRunnerEnabledUsesExpectedRunnerOutputs(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	caseID := "case"
	metricName := "metric"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, &evalset.EvalCase{
		EvalID:                caseID,
		ExpectedRunnerEnabled: true,
		Conversation: []*evalset.Invocation{
			makeActualInvocation("inv-1", "prompt", "static-expected"),
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

	actualRunner := &fakeRunner{events: []*event.Event{makeFinalEvent("actual")}}
	expectedRunner := &fakeRunner{events: []*event.Event{makeFinalEvent("expected")}}
	svc, err := New(
		actualRunner,
		service.WithEvalSetManager(mgr),
		service.WithRegistry(reg),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
		service.WithExpectedRunner(expectedRunner),
	)
	assert.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	inferenceResults, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, inferenceResults, 1)
	if assert.Len(t, inferenceResults, 1) {
		assert.Len(t, inferenceResults[0].ExpectedInferences, 1)
	}
	expectedRunner.mu.Lock()
	callsAfterInference := len(expectedRunner.calls)
	expectedRunner.mu.Unlock()
	assert.Equal(t, 1, callsAfterInference)

	runResult, err := svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: inferenceResults,
		EvaluateConfig: &service.EvaluateConfig{
			EvalMetrics: []*metric.EvalMetric{{MetricName: metricName, Threshold: 0.5}},
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, runResult)

	assert.Len(t, fakeEval.receivedActuals, 1)
	assert.NotNil(t, fakeEval.receivedActuals[0])
	assert.NotNil(t, fakeEval.receivedActuals[0].FinalResponse)
	assert.Equal(t, "actual", fakeEval.receivedActuals[0].FinalResponse.Content)

	assert.Len(t, fakeEval.receivedExpecteds, 1)
	assert.NotNil(t, fakeEval.receivedExpecteds[0])
	assert.NotNil(t, fakeEval.receivedExpecteds[0].FinalResponse)
	assert.Equal(t, "expected", fakeEval.receivedExpecteds[0].FinalResponse.Content)

	expectedRunner.mu.Lock()
	expectedCalls := len(expectedRunner.calls)
	expectedSessionID := ""
	if expectedCalls > 0 {
		expectedSessionID = expectedRunner.sessionIDs[0]
	}
	expectedRunner.mu.Unlock()
	assert.Equal(t, 1, expectedCalls)
	assert.Equal(t, "session-expected", expectedSessionID)
	actualRunner.mu.Lock()
	actualCalls := len(actualRunner.calls)
	actualSessionID := ""
	if actualCalls > 0 {
		actualSessionID = actualRunner.sessionIDs[0]
	}
	actualRunner.mu.Unlock()
	assert.Equal(t, 1, actualCalls)
	assert.Equal(t, "session", actualSessionID)
}

func TestLocalEvaluateConversationScenarioExpectedRunnerEnabledUsesActualUserSequence(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	caseID := "case"
	metricName := "metric"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, &evalset.EvalCase{
		EvalID:                caseID,
		ExpectedRunnerEnabled: true,
		ConversationScenario: &evalset.ConversationScenario{
			ConversationPlan: "Ask one follow-up and stop when done.",
			StopSignal:       "</finished>",
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

	actualRunner := &fakeRunner{events: []*event.Event{makeFinalEvent("actual")}}
	expectedRunner := &fakeRunner{events: []*event.Event{makeFinalEvent("expected")}}
	simulator := &scenarioTestSimulator{
		conversation: &scenarioTestConversation{
			decisions: []*usersimulation.Decision{
				{Message: &model.Message{Role: model.RoleUser, Content: "Please book tomorrow morning."}},
				{Message: &model.Message{Role: model.RoleUser, Content: "Use a refundable option and stay near line 2."}},
				{Stop: true},
			},
		},
	}
	svc, err := New(
		actualRunner,
		service.WithEvalSetManager(mgr),
		service.WithRegistry(reg),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
		service.WithExpectedRunner(expectedRunner),
		service.WithUserSimulator(simulator),
	)
	assert.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	inferenceResults, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, inferenceResults, 1)
	if assert.Len(t, inferenceResults, 1) {
		assert.Len(t, inferenceResults[0].ExpectedInferences, 2)
	}
	expectedRunner.mu.Lock()
	callsAfterInference := len(expectedRunner.calls)
	expectedRunner.mu.Unlock()
	assert.Equal(t, 2, callsAfterInference)

	runResult, err := svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: inferenceResults,
		EvaluateConfig: &service.EvaluateConfig{
			EvalMetrics: []*metric.EvalMetric{{MetricName: metricName, Threshold: 0.5}},
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, runResult)

	if assert.Len(t, fakeEval.receivedActuals, 2) {
		assert.Equal(t, "Please book tomorrow morning.", fakeEval.receivedActuals[0].UserContent.Content)
		assert.Equal(t, "Use a refundable option and stay near line 2.", fakeEval.receivedActuals[1].UserContent.Content)
		assert.Equal(t, "actual", fakeEval.receivedActuals[0].FinalResponse.Content)
		assert.Equal(t, "actual", fakeEval.receivedActuals[1].FinalResponse.Content)
	}
	if assert.Len(t, fakeEval.receivedExpecteds, 2) {
		assert.Equal(t, "Please book tomorrow morning.", fakeEval.receivedExpecteds[0].UserContent.Content)
		assert.Equal(t, "Use a refundable option and stay near line 2.", fakeEval.receivedExpecteds[1].UserContent.Content)
		assert.Equal(t, "expected", fakeEval.receivedExpecteds[0].FinalResponse.Content)
		assert.Equal(t, "expected", fakeEval.receivedExpecteds[1].FinalResponse.Content)
	}
	expectedRunner.mu.Lock()
	expectedCalls := len(expectedRunner.calls)
	expectedSessionID := ""
	if expectedCalls > 0 {
		expectedSessionID = expectedRunner.sessionIDs[0]
	}
	expectedMessages := append([]model.Message(nil), expectedRunner.calls...)
	expectedRunner.mu.Unlock()
	assert.Equal(t, 2, expectedCalls)
	assert.Equal(t, "session-expected", expectedSessionID)
	if assert.Len(t, expectedMessages, 2) {
		assert.Equal(t, "Please book tomorrow morning.", expectedMessages[0].Content)
		assert.Equal(t, "Use a refundable option and stay near line 2.", expectedMessages[1].Content)
	}
	actualRunner.mu.Lock()
	actualCalls := len(actualRunner.calls)
	actualSessionID := ""
	if actualCalls > 0 {
		actualSessionID = actualRunner.sessionIDs[0]
	}
	actualMessages := append([]model.Message(nil), actualRunner.calls...)
	actualRunner.mu.Unlock()
	assert.Equal(t, 2, actualCalls)
	assert.Equal(t, "session", actualSessionID)
	if assert.Len(t, actualMessages, 2) {
		assert.Equal(t, "Please book tomorrow morning.", actualMessages[0].Content)
		assert.Equal(t, "Use a refundable option and stay near line 2.", actualMessages[1].Content)
	}
}

func TestLocalEvaluateConversationScenarioExpectedDriverReusesPrecomputedExpecteds(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	caseID := "case"
	metricName := "metric"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, &evalset.EvalCase{
		EvalID:                caseID,
		ExpectedRunnerEnabled: true,
		ConversationScenario: &evalset.ConversationScenario{
			Driver:           evalset.ConversationScenarioDriverExpected,
			ConversationPlan: "Ask one follow-up and stop when done.",
			StopSignal:       "</finished>",
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
				{Score: 1, Status: status.EvalStatusPassed},
			},
		},
	}
	assert.NoError(t, reg.Register(metricName, fakeEval))

	actualRunner := &fakeRunner{events: []*event.Event{makeFinalEvent("actual")}}
	expectedRunner := &fakeRunner{events: []*event.Event{makeFinalEvent("expected")}}
	simulator := &scenarioTestSimulator{
		conversation: &scenarioTestConversation{
			decisions: []*usersimulation.Decision{
				{Message: &model.Message{Role: model.RoleUser, Content: "Please book tomorrow morning."}},
				{Message: &model.Message{Role: model.RoleUser, Content: "Use a refundable option and stay near line 2."}},
				{Stop: true},
			},
		},
	}
	svc, err := New(
		actualRunner,
		service.WithEvalSetManager(mgr),
		service.WithRegistry(reg),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
		service.WithExpectedRunner(expectedRunner),
		service.WithUserSimulator(simulator),
	)
	assert.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	inferenceResults, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, inferenceResults, 1)
	expectedRunner.mu.Lock()
	callsAfterInference := len(expectedRunner.calls)
	expectedMessagesAfterInference := append([]model.Message(nil), expectedRunner.calls...)
	expectedRunner.mu.Unlock()
	assert.Equal(t, 2, callsAfterInference)
	if assert.Len(t, expectedMessagesAfterInference, 2) {
		assert.Equal(t, "Please book tomorrow morning.", expectedMessagesAfterInference[0].Content)
		assert.Equal(t, "Use a refundable option and stay near line 2.", expectedMessagesAfterInference[1].Content)
	}

	runResult, err := svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: inferenceResults,
		EvaluateConfig: &service.EvaluateConfig{
			EvalMetrics: []*metric.EvalMetric{{MetricName: metricName, Threshold: 0.5}},
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, runResult)

	if assert.Len(t, fakeEval.receivedActuals, 2) {
		assert.Equal(t, "Please book tomorrow morning.", fakeEval.receivedActuals[0].UserContent.Content)
		assert.Equal(t, "Use a refundable option and stay near line 2.", fakeEval.receivedActuals[1].UserContent.Content)
		assert.Equal(t, "actual", fakeEval.receivedActuals[0].FinalResponse.Content)
		assert.Equal(t, "actual", fakeEval.receivedActuals[1].FinalResponse.Content)
	}
	if assert.Len(t, fakeEval.receivedExpecteds, 2) {
		assert.Equal(t, "Please book tomorrow morning.", fakeEval.receivedExpecteds[0].UserContent.Content)
		assert.Equal(t, "Use a refundable option and stay near line 2.", fakeEval.receivedExpecteds[1].UserContent.Content)
		assert.Equal(t, "expected", fakeEval.receivedExpecteds[0].FinalResponse.Content)
		assert.Equal(t, "expected", fakeEval.receivedExpecteds[1].FinalResponse.Content)
	}
	expectedRunner.mu.Lock()
	callsAfterEvaluate := len(expectedRunner.calls)
	expectedSessionID := ""
	if callsAfterEvaluate > 0 {
		expectedSessionID = expectedRunner.sessionIDs[0]
	}
	expectedRunner.mu.Unlock()
	assert.Equal(t, 2, callsAfterEvaluate)
	assert.Equal(t, "session-expected", expectedSessionID)
	actualRunner.mu.Lock()
	actualCalls := len(actualRunner.calls)
	actualSessionID := ""
	if actualCalls > 0 {
		actualSessionID = actualRunner.sessionIDs[0]
	}
	actualMessages := append([]model.Message(nil), actualRunner.calls...)
	actualRunner.mu.Unlock()
	assert.Equal(t, 2, actualCalls)
	assert.Equal(t, "session", actualSessionID)
	if assert.Len(t, actualMessages, 2) {
		assert.Equal(t, "Please book tomorrow morning.", actualMessages[0].Content)
		assert.Equal(t, "Use a refundable option and stay near line 2.", actualMessages[1].Content)
	}
}

func TestLocalInferenceExpectedRunnerEnabledWithoutExpectedRunnerMarksCaseFailed(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	caseID := "case"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, &evalset.EvalCase{
		EvalID:                caseID,
		ExpectedRunnerEnabled: true,
		Conversation: []*evalset.Invocation{
			makeInvocation("inv-1", "prompt"),
		},
		SessionInput: &evalset.SessionInput{AppName: appName, UserID: "demo-user", State: map[string]any{}},
	}))

	reg := registry.New()
	actualRunner := &fakeRunner{events: []*event.Event{makeFinalEvent("actual")}}
	svc, err := New(
		actualRunner,
		service.WithEvalSetManager(mgr),
		service.WithRegistry(reg),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	assert.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	inferenceResults, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	assert.Len(t, inferenceResults, 1)
	if assert.Len(t, inferenceResults, 1) {
		assert.Equal(t, status.EvalStatusFailed, inferenceResults[0].Status)
		assert.Contains(t, inferenceResults[0].ErrorMessage, "expected runner is nil")
	}
}

func TestLocalEvaluateExpectedRunnerEnabledWithoutExpectedInferencesMarksCaseFailed(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	caseID := "case"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, &evalset.EvalCase{
		EvalID:                caseID,
		ExpectedRunnerEnabled: true,
		Conversation: []*evalset.Invocation{
			makeInvocation("inv-1", "prompt"),
		},
		SessionInput: &evalset.SessionInput{AppName: appName, UserID: "demo-user", State: map[string]any{}},
	}))

	svc, err := New(
		&fakeRunner{},
		service.WithEvalSetManager(mgr),
		service.WithRegistry(registry.New()),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
	)
	assert.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	runResult, err := svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:   appName,
		EvalSetID: evalSetID,
		InferenceResults: []*service.InferenceResult{
			makeInferenceResult(appName, evalSetID, caseID, "session", []*evalset.Invocation{
				makeActualInvocation("inv-1", "prompt", "actual"),
			}),
		},
		EvaluateConfig: &service.EvaluateConfig{
			EvalMetrics: []*metric.EvalMetric{{MetricName: "missing_metric", Threshold: 0.5}},
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, runResult)
	if assert.Len(t, runResult.EvalCaseResults, 1) {
		assert.Equal(t, status.EvalStatusFailed, runResult.EvalCaseResults[0].FinalEvalStatus)
		assert.Contains(t, runResult.EvalCaseResults[0].ErrorMessage, "expected inferences are empty")
	}
}

func TestInferExpectedInferencesValidationErrors(t *testing.T) {
	ctx := context.Background()
	svc := &local{expectedRunner: &fakeRunner{}}
	evalCase := &evalset.EvalCase{
		EvalID: "case",
		SessionInput: &evalset.SessionInput{
			AppName: "app",
			UserID:  "demo-user",
			State:   map[string]any{},
		},
	}
	tests := []struct {
		name   string
		inputs []*evalset.Invocation
		want   string
	}{
		{name: "empty_inputs", inputs: []*evalset.Invocation{}, want: "input invocations are empty"},
		{name: "nil_input", inputs: []*evalset.Invocation{nil}, want: "input invocation is nil at index 0"},
		{name: "nil_user_content", inputs: []*evalset.Invocation{{InvocationID: "inv-1"}}, want: "input invocation user content is nil at index 0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.inferExpectedInferences(ctx, evalCase, tc.inputs, "session", &service.Options{
				ExpectedRunner: svc.expectedRunner,
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestInferExpectedInferencesPropagatesRunnerError(t *testing.T) {
	ctx := context.Background()
	svc := &local{expectedRunner: &fakeRunner{err: errors.New("run failed")}}
	evalCase := &evalset.EvalCase{
		EvalID: "case",
		SessionInput: &evalset.SessionInput{
			AppName: "app",
			UserID:  "demo-user",
			State:   map[string]any{},
		},
	}
	_, err := svc.inferExpectedInferences(ctx, evalCase, []*evalset.Invocation{makeInvocation("inv-1", "prompt")}, "session", &service.Options{
		ExpectedRunner: svc.expectedRunner,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "run expected runner")
	assert.Contains(t, err.Error(), "run failed")
}

func TestLocalInferenceTraceModeExpectedRunnerEnabledPrecomputesExpecteds(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	caseID := "case"

	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, &evalset.EvalCase{
		EvalID:                caseID,
		EvalMode:              evalset.EvalModeTrace,
		ExpectedRunnerEnabled: true,
		ActualConversation: []*evalset.Invocation{
			makeActualInvocation("inv-1", "prompt", "recorded-actual"),
		},
		SessionInput: &evalset.SessionInput{AppName: appName, UserID: "demo-user", State: map[string]any{}},
	}))

	actualRunner := &fakeRunner{err: errors.New("actual runner should not be called")}
	expectedRunner := &fakeRunner{events: []*event.Event{makeFinalEvent("expected")}}
	svc, err := New(
		actualRunner,
		service.WithEvalSetManager(mgr),
		service.WithRegistry(registry.New()),
		service.WithSessionIDSupplier(func(ctx context.Context) string { return "session" }),
		service.WithExpectedRunner(expectedRunner),
	)
	assert.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })

	inferenceResults, err := svc.Inference(ctx, &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID})
	assert.NoError(t, err)
	if assert.Len(t, inferenceResults, 1) {
		assert.Equal(t, status.EvalStatusPassed, inferenceResults[0].Status)
		if assert.Len(t, inferenceResults[0].ExpectedInferences, 1) {
			assert.Equal(t, "expected", inferenceResults[0].ExpectedInferences[0].FinalResponse.Content)
		}
	}
	actualRunner.mu.Lock()
	actualCalls := len(actualRunner.calls)
	actualRunner.mu.Unlock()
	assert.Zero(t, actualCalls)
	expectedRunner.mu.Lock()
	expectedCalls := len(expectedRunner.calls)
	expectedSessionID := ""
	if expectedCalls > 0 {
		expectedSessionID = expectedRunner.sessionIDs[0]
	}
	expectedRunner.mu.Unlock()
	assert.Equal(t, 1, expectedCalls)
	assert.Equal(t, "session-expected", expectedSessionID)
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
		opts:     &service.Options{SessionIDSupplier: func(ctx context.Context) string { return "session" }},
		svc:      &local{},
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

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-1", SessionID: "session", Status: status.EvalStatusFailed, ErrorMessage: "failed"}},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	assert.NoError(t, err)
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

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:        appName,
		EvalSetID:      evalSetID,
		EvaluateConfig: &service.EvaluateConfig{},
	})
	assert.Error(t, err)
	if err == nil {
		return
	}
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

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-1", SessionID: "session", Status: status.EvalStatusFailed, ErrorMessage: "failed"}},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	assert.NoError(t, err)
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

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-1", SessionID: "session", Status: status.EvalStatusFailed, ErrorMessage: "failed"}},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	assert.Error(t, err)
	if err == nil {
		return
	}
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

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-1", SessionID: "session", Status: status.EvalStatusFailed, ErrorMessage: "failed"}},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	assert.Error(t, err)
	if err == nil {
		return
	}
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
	err := svc.runAfterEvaluateCaseCallbacks(ctx, callbacks, &service.EvaluateRequest{AppName: "app", EvalSetID: "set", EvaluateConfig: &service.EvaluateConfig{}}, nil, nil, nil, time.Unix(123, 0))
	assert.Error(t, err)
	if err == nil {
		return
	}
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

	_, err = svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{nil},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	assert.Error(t, err)
	if err == nil {
		return
	}
	assert.Contains(t, err.Error(), "evaluate case")
	assert.Contains(t, err.Error(), "evalCaseID=")
	assert.Contains(t, err.Error(), "inference result is nil")
}

func TestLocalEvaluateDoesNotPersistEvalSetResult(t *testing.T) {
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

	res, err := svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-1", SessionID: "session", Status: status.EvalStatusFailed, ErrorMessage: "failed"}},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	assert.NoError(t, err)
	assert.NotNil(t, res)
	if res == nil {
		return
	}
	assert.Len(t, res.EvalCaseResults, 1)
	assert.Equal(t, "case-1", res.EvalCaseResults[0].EvalID)
	assert.Equal(t, status.EvalStatusFailed, res.EvalCaseResults[0].FinalEvalStatus)
	assert.Equal(t, "failed", res.EvalCaseResults[0].ErrorMessage)
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

	result, err := svc.Evaluate(ctx, &service.EvaluateRequest{
		AppName:          appName,
		EvalSetID:        evalSetID,
		InferenceResults: []*service.InferenceResult{{AppName: appName, EvalSetID: evalSetID, EvalCaseID: "case-1", SessionID: "session", Status: status.EvalStatusPassed}},
		EvaluateConfig:   &service.EvaluateConfig{},
	})
	assert.NoError(t, err)
	if err != nil {
		return
	}
	assert.NotNil(t, result)
	if result == nil {
		return
	}
	assert.Len(t, result.EvalCaseResults, 1)
	if len(result.EvalCaseResults) != 1 {
		return
	}
	assert.Equal(t, status.EvalStatusFailed, result.EvalCaseResults[0].FinalEvalStatus)
	assert.Contains(t, result.EvalCaseResults[0].ErrorMessage, "get eval case")
	assert.Contains(t, result.EvalCaseResults[0].ErrorMessage, "get case failed")
}

func TestRunAfterEvaluateSetCallbacksPassesArgs(t *testing.T) {
	ctx := context.Background()
	startTime := time.Unix(123, 0)
	wantErr := errors.New("evaluate error")
	req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set", EvaluateConfig: &service.EvaluateConfig{}}
	result := &service.EvalSetRunResult{AppName: "app", EvalSetID: "set"}

	callbacks := service.NewCallbacks()
	var got *service.AfterEvaluateSetArgs
	callbacks.RegisterAfterEvaluateSet("probe", func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
		got = args
		return nil, nil
	})

	svc := &local{callbacks: callbacks}
	err := svc.runAfterEvaluateSetCallbacks(ctx, callbacks, req, result, wantErr, startTime)
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Same(t, req, got.Request)
	assert.Same(t, result, got.Result)
	assert.Same(t, wantErr, got.Error)
	assert.Equal(t, startTime, got.StartTime)
}

func TestLocalEvaluateAfterEvaluateSetReceivesNilResultWhenEvaluateFails(t *testing.T) {
	ctx := context.Background()
	req := &service.EvaluateRequest{
		AppName:          "app",
		EvalSetID:        "set",
		InferenceResults: []*service.InferenceResult{nil},
		EvaluateConfig:   &service.EvaluateConfig{},
	}

	callbacks := service.NewCallbacks()
	var got *service.AfterEvaluateSetArgs
	callbacks.RegisterAfterEvaluateSet("probe", func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
		got = args
		return nil, nil
	})

	svc := &local{
		callbacks:      callbacks,
		evalSetManager: evalsetinmemory.New(),
		registry:       registry.New(),
		metricRegistry: metricregistry.New(),
	}
	_, err := svc.Evaluate(ctx, req)
	assert.Error(t, err)
	assert.NotNil(t, got)
	if got == nil {
		return
	}
	assert.Same(t, req, got.Request)
	assert.Nil(t, got.Result)
	assert.Error(t, got.Error)
}

func TestLocalEvaluateAfterEvaluateSetReceivesRunResultOnSuccess(t *testing.T) {
	ctx := context.Background()
	req := &service.EvaluateRequest{
		AppName:   "app",
		EvalSetID: "set",
		InferenceResults: []*service.InferenceResult{
			{
				AppName:      "app",
				EvalSetID:    "set",
				EvalCaseID:   "case-1",
				SessionID:    "session",
				UserID:       "user",
				Status:       status.EvalStatusFailed,
				ErrorMessage: "inference failed",
			},
		},
		EvaluateConfig: &service.EvaluateConfig{},
	}

	callbacks := service.NewCallbacks()
	var got *service.AfterEvaluateSetArgs
	callbacks.RegisterAfterEvaluateSet("probe", func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
		got = args
		return nil, nil
	})

	svc := &local{
		callbacks:      callbacks,
		evalSetManager: evalsetinmemory.New(),
		registry:       registry.New(),
		metricRegistry: metricregistry.New(),
	}
	res, err := svc.Evaluate(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, res)
	assert.NotNil(t, got)
	if got == nil {
		return
	}
	assert.Same(t, req, got.Request)
	assert.Same(t, res, got.Result)
	assert.NoError(t, got.Error)
}

func TestRunAfterEvaluateSetCallbacksWrapsErrorWithContext(t *testing.T) {
	ctx := context.Background()
	startTime := time.Unix(123, 0)
	req := &service.EvaluateRequest{AppName: "app", EvalSetID: "set", EvaluateConfig: &service.EvaluateConfig{}}
	sentinel := errors.New("boom")

	callbacks := service.NewCallbacks()
	callbacks.RegisterAfterEvaluateSet("bad", func(ctx context.Context, args *service.AfterEvaluateSetArgs) (*service.AfterEvaluateSetResult, error) {
		return nil, sentinel
	})

	svc := &local{callbacks: callbacks}
	err := svc.runAfterEvaluateSetCallbacks(ctx, callbacks, req, nil, nil, startTime)
	assert.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "run after evaluate set callbacks")
	assert.Contains(t, err.Error(), "app=app")
	assert.Contains(t, err.Error(), "evalSetID=set")
}

type failingEvalResultManager struct {
	err error
}

func (m *failingEvalResultManager) Close() error {
	return nil
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

func TestPrepareCaseEvaluationInputs_AttachesContextMessagesToEachInvocation(t *testing.T) {
	ctx := context.Background()
	contextMessages := []*model.Message{
		{Role: model.RoleSystem, Content: "system context"},
		{Role: model.RoleUser, Content: "previous user message"},
	}

	inferenceResult := &service.InferenceResult{
		AppName:    "app",
		EvalSetID:  "set",
		EvalCaseID: "case",
		Inferences: []*evalset.Invocation{
			{InvocationID: "1", UserContent: &model.Message{Role: model.RoleUser, Content: "u1"}, FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "a1"}},
			{InvocationID: "2", UserContent: &model.Message{Role: model.RoleUser, Content: "u2"}, FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "a2"}},
		},
	}

	evalCase := &evalset.EvalCase{
		EvalID:          "case",
		ContextMessages: contextMessages,
		Conversation: []*evalset.Invocation{
			{InvocationID: "1", UserContent: &model.Message{Role: model.RoleUser, Content: "u1"}, FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "e1"}},
			{InvocationID: "2", UserContent: &model.Message{Role: model.RoleUser, Content: "u2"}, FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "e2"}},
		},
		SessionInput: &evalset.SessionInput{UserID: "u"},
	}

	svc := &local{}
	inputs, err := svc.prepareCaseEvaluationInputs(ctx, inferenceResult, evalCase, &service.Options{})
	assert.NoError(t, err)
	assert.Len(t, inputs.actuals, 2)
	assert.Len(t, inputs.expecteds, 2)
	for _, invocation := range inputs.actuals {
		assert.Equal(t, contextMessages, invocation.ContextMessages)
	}
	for _, invocation := range inputs.expecteds {
		assert.Equal(t, contextMessages, invocation.ContextMessages)
	}
}

func TestAttachContextMessages_SkipsNilAndPrePopulatedInvocations(t *testing.T) {
	contextMessages := []*model.Message{
		{Role: model.RoleSystem, Content: "system context"},
	}
	existing := []*model.Message{
		{Role: model.RoleSystem, Content: "existing context"},
	}
	invWithExisting := &evalset.Invocation{
		InvocationID:    "1",
		ContextMessages: existing,
		UserContent:     &model.Message{Role: model.RoleUser, Content: "u1"},
	}
	invEmpty := &evalset.Invocation{
		InvocationID: "2",
		UserContent:  &model.Message{Role: model.RoleUser, Content: "u2"},
	}
	invocations := []*evalset.Invocation{nil, invWithExisting, invEmpty}

	attachContextMessages(invocations, contextMessages)

	assert.Equal(t, existing, invWithExisting.ContextMessages)
	assert.Equal(t, contextMessages, invEmpty.ContextMessages)
}

func TestPrepareCaseEvaluationInputsScenarioBuildsPlaceholderExpecteds(t *testing.T) {
	systemMessage := model.NewSystemMessage("You are a helper.")
	evalCase := makeScenarioEvalCase("app", "case-1")
	evalCase.ContextMessages = []*model.Message{&systemMessage}
	inferenceResult := makeInferenceResult("app", "set", "case-1", "session-1", []*evalset.Invocation{
		makeActualInvocation("inv-1", "user prompt", "assistant reply"),
	})
	svc := &local{}
	inputs, err := svc.prepareCaseEvaluationInputs(context.Background(), inferenceResult, evalCase, &service.Options{})
	assert.NoError(t, err)
	assert.Len(t, inputs.actuals, 1)
	assert.Len(t, inputs.expecteds, 1)
	assert.Equal(t, "demo-user", inputs.userID)
	if assert.NotNil(t, inputs.expecteds[0]) {
		assert.Equal(t, "inv-1", inputs.expecteds[0].InvocationID)
		assert.Same(t, inputs.actuals[0].UserContent, inputs.expecteds[0].UserContent)
		assert.Nil(t, inputs.expecteds[0].FinalResponse)
		assert.Len(t, inputs.expecteds[0].ContextMessages, 1)
	}
	if assert.NotNil(t, inputs.actuals[0]) {
		assert.Len(t, inputs.actuals[0].ContextMessages, 1)
	}
}

func TestEvaluatePerCaseScenarioRunsConfiguredMetric(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	evalCase := makeScenarioEvalCase(appName, "case-1")
	mgr := evalsetinmemory.New()
	_, err := mgr.Create(ctx, appName, evalSetID)
	assert.NoError(t, err)
	assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, evalCase))
	reg := registry.New()
	fakeEval := &fakeEvaluator{
		name: "llm_rubric_response",
		result: &evaluator.EvaluateResult{
			OverallScore:  1,
			OverallStatus: status.EvalStatusPassed,
			PerInvocationResults: []*evaluator.PerInvocationResult{
				{Score: 1, Status: status.EvalStatusPassed},
			},
		},
	}
	assert.NoError(t, reg.Register(fakeEval.name, fakeEval))
	svc := newLocalService(t, &fakeRunner{}, mgr, reg, "session-1")
	inferenceResult := makeInferenceResult(appName, evalSetID, "case-1", "session-1", []*evalset.Invocation{
		makeActualInvocation("inv-1", "user prompt", "assistant reply"),
	})
	result, err := svc.evaluatePerCase(ctx, inferenceResult, &service.EvaluateConfig{
		EvalMetrics: []*metric.EvalMetric{
			{MetricName: "llm_rubric_response"},
		},
	}, &service.Options{
		EvalSetManager: mgr,
		Registry:       reg,
	})
	assert.NoError(t, err)
	assert.Equal(t, status.EvalStatusPassed, result.FinalEvalStatus)
	assert.Len(t, result.OverallEvalMetricResults, 1)
	if assert.Len(t, result.EvalMetricResultPerInvocation, 1) {
		assert.Len(t, result.EvalMetricResultPerInvocation[0].EvalMetricResults, 1)
		assert.Equal(t, status.EvalStatusPassed, result.EvalMetricResultPerInvocation[0].EvalMetricResults[0].EvalStatus)
	}
	assert.Equal(t, status.EvalStatusPassed, result.OverallEvalMetricResults[0].EvalStatus)
	assert.Equal(t, inferenceResult.Inferences, fakeEval.receivedActuals)
	assert.Len(t, fakeEval.receivedExpecteds, 1)
}

func TestLocalEvaluateMetricUsesEvaluatorNameWhenPresent(t *testing.T) {
	ctx := context.Background()
	reg := registry.New()
	fakeEval := &fakeEvaluator{
		name: "llm_judge_template",
		result: &evaluator.EvaluateResult{
			OverallScore:         1,
			OverallStatus:        status.EvalStatusPassed,
			PerInvocationResults: []*evaluator.PerInvocationResult{{Score: 1, Status: status.EvalStatusPassed}},
		},
	}
	assert.NoError(t, reg.Register(fakeEval.name, fakeEval))
	svc := &local{}
	actuals := []*evalset.Invocation{makeActualInvocation("actual-1", "prompt", "answer")}
	expecteds := []*evalset.Invocation{makeActualInvocation("expected-1", "prompt", "reference")}
	evalMetric := &metric.EvalMetric{
		MetricName:    "answer_quality_against_reference",
		EvaluatorName: fakeEval.name,
		Threshold:     0.5,
	}

	result, err := svc.evaluateMetric(ctx, reg, evalMetric, actuals, expecteds)
	assert.NoError(t, err)
	assert.Equal(t, fakeEval.result, result)
	assert.Equal(t, actuals, fakeEval.receivedActuals)
	assert.Equal(t, expecteds, fakeEval.receivedExpecteds)
}

func TestMaterializeResultCriterionInstantiatesTemplatePrompt(t *testing.T) {
	ctx := context.Background()
	evalMetric := &metric.EvalMetric{
		MetricName:    "answer_quality_against_reference",
		EvaluatorName: llmtemplateevaluator.EvaluatorName,
		Threshold:     1.0,
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Template: &criterionllm.JudgeTemplateOptions{
					Prompt:             "Question: {{question}}\nAnswer: {{answer}}\nReference: {{reference}}",
					ResponseScorerName: "single_score",
					VariableBindings: []*criterionllm.TemplateVariableBinding{
						{
							TemplateVariable: "question",
							Source: &criterionllm.TemplateVariableSource{
								Scope: criterionllm.TemplateVariableScopeActual,
								Field: criterionllm.TemplateVariableFieldUserContent,
							},
						},
						{
							TemplateVariable: "answer",
							Source: &criterionllm.TemplateVariableSource{
								Scope: criterionllm.TemplateVariableScopeActual,
								Field: criterionllm.TemplateVariableFieldFinalResponse,
							},
						},
						{
							TemplateVariable: "reference",
							Source: &criterionllm.TemplateVariableSource{
								Scope: criterionllm.TemplateVariableScopeExpected,
								Field: criterionllm.TemplateVariableFieldFinalResponse,
							},
						},
					},
				},
			},
		},
	}
	actuals := []*evalset.Invocation{{
		UserContent:   &model.Message{Role: model.RoleUser, Content: "What is the capital of France?"},
		FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "Paris"},
	}}
	expecteds := []*evalset.Invocation{{
		FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "Paris"},
	}}

	got, err := materializeResultCriterion(ctx, evalMetric, actuals, expecteds)
	assert.NoError(t, err)
	if assert.NotNil(t, got) && assert.NotNil(t, got.LLMJudge) && assert.NotNil(t, got.LLMJudge.Template) {
		assert.Equal(t,
			"Question: What is the capital of France?\nAnswer: Paris\nReference: Paris",
			got.LLMJudge.Template.Prompt,
		)
	}
	assert.Equal(t,
		"Question: {{question}}\nAnswer: {{answer}}\nReference: {{reference}}",
		evalMetric.Criterion.LLMJudge.Template.Prompt,
	)
}

func TestMaterializeOverallCriterionKeepsTemplatePrompt(t *testing.T) {
	ctx := context.Background()
	evalMetric := &metric.EvalMetric{
		MetricName:    "answer_quality_against_reference",
		EvaluatorName: llmtemplateevaluator.EvaluatorName,
		Threshold:     1.0,
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Template: &criterionllm.JudgeTemplateOptions{
					Prompt:             "Question: {{question}}\nAnswer: {{answer}}\nReference: {{reference}}",
					ResponseScorerName: "single_score",
					VariableBindings: []*criterionllm.TemplateVariableBinding{
						{
							TemplateVariable: "question",
							Source: &criterionllm.TemplateVariableSource{
								Scope: criterionllm.TemplateVariableScopeActual,
								Field: criterionllm.TemplateVariableFieldUserContent,
							},
						},
						{
							TemplateVariable: "answer",
							Source: &criterionllm.TemplateVariableSource{
								Scope: criterionllm.TemplateVariableScopeActual,
								Field: criterionllm.TemplateVariableFieldFinalResponse,
							},
						},
						{
							TemplateVariable: "reference",
							Source: &criterionllm.TemplateVariableSource{
								Scope: criterionllm.TemplateVariableScopeExpected,
								Field: criterionllm.TemplateVariableFieldFinalResponse,
							},
						},
					},
				},
			},
		},
	}
	actuals := []*evalset.Invocation{{
		UserContent:   &model.Message{Role: model.RoleUser, Content: "What is the capital of France?"},
		FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "Paris"},
	}}
	expecteds := []*evalset.Invocation{{
		FinalResponse: &model.Message{Role: model.RoleAssistant, Content: "Paris"},
	}}

	got, err := materializeOverallCriterion(ctx, evalMetric, actuals, expecteds)
	assert.NoError(t, err)
	if assert.NotNil(t, got) && assert.NotNil(t, got.LLMJudge) && assert.NotNil(t, got.LLMJudge.Template) {
		assert.Equal(t,
			"Question: {{question}}\nAnswer: {{answer}}\nReference: {{reference}}",
			got.LLMJudge.Template.Prompt,
		)
	}
}

func TestMaterializeResultCriterionHandlesErrorsAndNonTemplateEvaluator(t *testing.T) {
	ctx := context.Background()
	got, err := materializeResultCriterion(ctx, nil, nil, nil)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "clone eval metric")
	evalMetric := &metric.EvalMetric{
		MetricName:    "answer_quality_against_reference",
		EvaluatorName: "llm_rubric_critic",
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Template: &criterionllm.JudgeTemplateOptions{
					Prompt:             "Question: {{question}}",
					ResponseScorerName: "single_score",
				},
			},
		},
	}
	got, err = materializeResultCriterion(ctx, evalMetric, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.LLMJudge)
	require.NotNil(t, got.LLMJudge.Template)
	assert.Equal(t, "Question: {{question}}", got.LLMJudge.Template.Prompt)
}

func TestMaterializeResultCriterionRejectsTemplateConstructionError(t *testing.T) {
	ctx := context.Background()
	evalMetric := &metric.EvalMetric{
		MetricName:    "answer_quality_against_reference",
		EvaluatorName: llmtemplateevaluator.EvaluatorName,
		Criterion: &criterion.Criterion{
			LLMJudge: &criterionllm.LLMCriterion{
				Template: &criterionllm.JudgeTemplateOptions{
					Prompt:             "Question: {{question}}",
					ResponseScorerName: "single_score",
					VariableBindings: []*criterionllm.TemplateVariableBinding{
						{
							TemplateVariable: "question",
							Source: &criterionllm.TemplateVariableSource{
								Scope: criterionllm.TemplateVariableScopeActual,
								Field: criterionllm.TemplateVariableFieldUserContent,
							},
						},
					},
				},
			},
		},
	}
	got, err := materializeResultCriterion(ctx, evalMetric, nil, nil)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "construct template messages")
}

func TestMaterializeOverallCriterionCloneError(t *testing.T) {
	got, err := materializeOverallCriterion(context.Background(), nil, nil, nil)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "clone eval metric")
}

func TestMaterializedPrompt(t *testing.T) {
	assert.Empty(t, materializedPrompt(nil))
	assert.Equal(t, "single", materializedPrompt([]model.Message{{
		Role:    model.RoleUser,
		Content: "single",
	}}))
	assert.Equal(t,
		"[user]\nquestion\n\n[assistant]\nanswer",
		materializedPrompt([]model.Message{
			{Role: model.RoleUser, Content: "question"},
			{Role: model.RoleAssistant, Content: "answer"},
		}),
	)
}

func TestResolveEvaluatorName(t *testing.T) {
	assert.Empty(t, resolveEvaluatorName(nil))
	assert.Equal(t, "metric_only", resolveEvaluatorName(&metric.EvalMetric{
		MetricName: "metric_only",
	}))
	assert.Equal(t, "llm_judge_template", resolveEvaluatorName(&metric.EvalMetric{
		MetricName:    "metric_only",
		EvaluatorName: "llm_judge_template",
	}))
}
