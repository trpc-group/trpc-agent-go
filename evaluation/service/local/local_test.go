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

	"github.com/stretchr/testify/assert"
	"google.golang.org/genai"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
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

type fakeEvaluator struct {
	name   string
	result *evaluator.EvaluateResult
	err    error
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
		UserContent: &genai.Content{
			Role:  "user",
			Parts: []*genai.Part{{Text: prompt}},
		},
	}
}

func makeActualInvocation(id, prompt, response string) *evalset.Invocation {
	inv := makeInvocation(id, prompt)
	inv.FinalResponse = &genai.Content{
		Role:  "assistant",
		Parts: []*genai.Part{{Text: response}},
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
	assert.Equal(t, status.EvalStatusPassed, results[0].Status)
	assert.Len(t, results[0].Inferences, 1)
	assert.NotNil(t, results[0].Inferences[0].FinalResponse)
	assert.Equal(t, "calc result: 3", results[0].Inferences[0].FinalResponse.Parts[0].Text)

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
	assert.Error(t, err)
	assert.Nil(t, results)
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
			PerInvocationResults: []evaluator.PerInvocationResult{
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
		name  string
		setup func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig)
	}{
		{
			name: "nil inference result",
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, _, _ := prepare(t)
				return svc, nil, &service.EvaluateConfig{}
			},
		},
		{
			name: "nil evaluate config",
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, _, _ := prepare(t)
				inference := makeInferenceResult(appName, evalSetID, "case", "session", nil)
				return svc, inference, nil
			},
		},
		{
			name: "missing eval case",
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, _, _ := prepare(t)
				inference := makeInferenceResult(appName, evalSetID, "missing", "session", []*evalset.Invocation{})
				config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}}
				return svc, inference, config
			},
		},
		{
			name: "invalid eval case",
			setup: func(t *testing.T) (*local, *service.InferenceResult, *service.EvaluateConfig) {
				svc, mgr, _ := prepare(t)
				_, err := mgr.Create(ctx, appName, evalSetID)
				assert.NoError(t, err)
				invalid := &evalset.EvalCase{
					EvalID:       "invalid",
					Conversation: []*evalset.Invocation{makeInvocation("invalid-1", "prompt")},
					SessionInput: nil,
				}
				assert.NoError(t, mgr.AddCase(ctx, appName, evalSetID, invalid))
				actual := makeActualInvocation("actual-1", "prompt", "answer")
				inference := makeInferenceResult(appName, evalSetID, "invalid", "session", []*evalset.Invocation{actual})
				config := &service.EvaluateConfig{EvalMetrics: []*metric.EvalMetric{}}
				return svc, inference, config
			},
		},
		{
			name: "mismatched inference count",
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
			name: "missing evaluator",
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
			name: "per invocation mismatch",
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
						PerInvocationResults: []evaluator.PerInvocationResult{},
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
			name: "summarize failure",
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
						PerInvocationResults: []evaluator.PerInvocationResult{{Score: 0, Status: status.EvalStatusNotEvaluated}},
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
			name: "evaluator error",
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
			assert.Error(t, err)
		})
	}
}
