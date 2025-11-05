//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evaluation

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type stubRunner struct{}

func (stubRunner) Run(ctx context.Context, userID string, sessionID string, message model.Message, runOpts ...agent.RunOption) (<-chan *event.Event, error) {
	return nil, nil
}

type fakeService struct {
	inferenceResults [][]*service.InferenceResult
	evaluateResults  []*evalresult.EvalSetResult
	inferenceErr     error
	evaluateErr      error

	inferenceRequests []*service.InferenceRequest
	evaluateRequests  []*service.EvaluateRequest
}

func (f *fakeService) Inference(ctx context.Context, req *service.InferenceRequest) ([]*service.InferenceResult, error) {
	f.inferenceRequests = append(f.inferenceRequests, req)
	if f.inferenceErr != nil {
		return nil, f.inferenceErr
	}
	idx := len(f.inferenceRequests) - 1
	if idx >= 0 && idx < len(f.inferenceResults) {
		return f.inferenceResults[idx], nil
	}
	return []*service.InferenceResult{}, nil
}

func (f *fakeService) Evaluate(ctx context.Context, req *service.EvaluateRequest) (*evalresult.EvalSetResult, error) {
	f.evaluateRequests = append(f.evaluateRequests, req)
	if f.evaluateErr != nil {
		return nil, f.evaluateErr
	}
	idx := len(f.evaluateRequests) - 1
	if idx >= 0 && idx < len(f.evaluateResults) {
		return f.evaluateResults[idx], nil
	}
	return &evalresult.EvalSetResult{
		EvalSetID:       req.EvalSetID,
		EvalCaseResults: []*evalresult.EvalCaseResult{},
	}, nil
}

type fakeMetricManager struct {
	listErr error
	getErr  error
	metrics map[string]*metric.EvalMetric
}

func (f *fakeMetricManager) List(ctx context.Context, appName, evalSetID string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	names := make([]string, 0, len(f.metrics))
	for name := range f.metrics {
		names = append(names, name)
	}
	return names, nil
}

func (f *fakeMetricManager) Save(ctx context.Context, appName, evalSetID string, metrics []*metric.EvalMetric) error {
	return nil
}

func (f *fakeMetricManager) Get(ctx context.Context, appName, evalSetID, metricName string) (*metric.EvalMetric, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if metric, ok := f.metrics[metricName]; ok {
		return metric, nil
	}
	return nil, errors.New("metric not found")
}

func (f *fakeMetricManager) Add(ctx context.Context, appName, evalSetID string, metricValue *metric.EvalMetric) error {
	if metricValue == nil {
		return errors.New("metric is nil")
	}
	if f.metrics == nil {
		f.metrics = make(map[string]*metric.EvalMetric)
	}
	f.metrics[metricValue.MetricName] = metricValue
	return nil
}

func (f *fakeMetricManager) Delete(ctx context.Context, appName, evalSetID, metricName string) error {
	delete(f.metrics, metricName)
	return nil
}

func (f *fakeMetricManager) Update(ctx context.Context, appName, evalSetID string, metricValue *metric.EvalMetric) error {
	if metricValue == nil {
		return errors.New("metric is nil")
	}
	if f.metrics == nil {
		f.metrics = make(map[string]*metric.EvalMetric)
	}
	f.metrics[metricValue.MetricName] = metricValue
	return nil
}

func makeEvalMetricResult(metricName string, score float64, status status.EvalStatus, threshold float64) *evalresult.EvalMetricResult {
	return &evalresult.EvalMetricResult{
		MetricName: metricName,
		Score:      score,
		EvalStatus: status,
		Threshold:  threshold,
	}
}

func makeEvalCaseResult(evalSetID, caseID string, metricName string, score float64, threshold float64, status status.EvalStatus) *evalresult.EvalCaseResult {
	return &evalresult.EvalCaseResult{
		EvalSetID:       evalSetID,
		EvalID:          caseID,
		FinalEvalStatus: status,
		OverallEvalMetricResults: []*evalresult.EvalMetricResult{
			makeEvalMetricResult(metricName, score, status, threshold),
		},
		EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{
			{
				EvalMetricResults: []*evalresult.EvalMetricResult{
					makeEvalMetricResult(metricName, score, status, threshold),
				},
			},
		},
		SessionID: "session",
		UserID:    "user",
	}
}

func TestNewAgentEvaluatorValidation(t *testing.T) {
	_, err := New("app", nil)
	assert.Error(t, err)

	_, err = New("app", stubRunner{}, WithNumRuns(0))
	assert.Error(t, err)

	ae, err := New("app", stubRunner{})
	assert.NoError(t, err)
	impl, ok := ae.(*agentEvaluator)
	assert.True(t, ok)
	assert.NotNil(t, impl.evalService)
}

func TestNewAgentEvaluatorWithCustomService(t *testing.T) {
	customSvc := &fakeService{}
	ae, err := New("app", stubRunner{}, WithEvaluationService(customSvc))
	assert.NoError(t, err)
	impl, ok := ae.(*agentEvaluator)
	assert.True(t, ok)
	assert.Equal(t, customSvc, impl.evalService)
}

func TestAgentEvaluatorEvaluateSuccess(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	caseID := "case-1"
	metricName := "metric"

	metricMgr := metricinmemory.New()
	err := metricMgr.Add(ctx, appName, evalSetID, &metric.EvalMetric{MetricName: metricName, Threshold: 1})
	assert.NoError(t, err)

	svc := &fakeService{
		inferenceResults: [][]*service.InferenceResult{
			{{
				AppName:    appName,
				EvalSetID:  evalSetID,
				EvalCaseID: caseID,
				Inferences: []*evalset.Invocation{{InvocationID: "inv-1"}},
				SessionID:  "session-1",
				Status:     status.EvalStatusPassed,
			}},
			{{
				AppName:    appName,
				EvalSetID:  evalSetID,
				EvalCaseID: caseID,
				Inferences: []*evalset.Invocation{{InvocationID: "inv-2"}},
				SessionID:  "session-2",
				Status:     status.EvalStatusPassed,
			}},
		},
		evaluateResults: []*evalresult.EvalSetResult{
			{
				EvalSetID:       evalSetID,
				EvalSetResultID: "result-1",
				EvalCaseResults: []*evalresult.EvalCaseResult{
					makeEvalCaseResult(evalSetID, caseID, metricName, 0.5, 1, status.EvalStatusFailed),
				},
			},
			{
				EvalSetID:       evalSetID,
				EvalSetResultID: "result-2",
				EvalCaseResults: []*evalresult.EvalCaseResult{
					makeEvalCaseResult(evalSetID, caseID, metricName, 1.5, 1, status.EvalStatusPassed),
				},
			},
		},
	}

	ae, err := New(
		appName,
		stubRunner{},
		WithMetricManager(metricMgr),
		WithEvalSetManager(evalsetinmemory.New()),
		WithRegistry(registry.New()),
		WithEvaluationService(svc),
		WithNumRuns(2),
	)
	assert.NoError(t, err)

	evaluationResult, err := ae.Evaluate(ctx, evalSetID)
	assert.NoError(t, err)
	assert.Equal(t, evalSetID, evaluationResult.EvalSetID)
	assert.Len(t, evaluationResult.EvalCases, 1)
	assert.Equal(t, status.EvalStatusPassed, evaluationResult.OverallStatus)

	caseResult := evaluationResult.EvalCases[0]
	assert.Equal(t, caseID, caseResult.EvalCaseID)
	assert.Equal(t, status.EvalStatusPassed, caseResult.OverallStatus)
	assert.Len(t, caseResult.MetricResults, 1)
	assert.InDelta(t, 1.0, caseResult.MetricResults[0].Score, 0.001)

	assert.Len(t, svc.inferenceRequests, 2)
	assert.Len(t, svc.evaluateRequests, 2)
	assert.Equal(t, appName, svc.evaluateRequests[0].AppName)
	assert.Equal(t, evalSetID, svc.evaluateRequests[0].EvalSetID)
	assert.NotNil(t, svc.evaluateRequests[0].EvaluateConfig)
	if svc.evaluateRequests[0].EvaluateConfig != nil {
		assert.Len(t, svc.evaluateRequests[0].EvaluateConfig.EvalMetrics, 1)
		assert.Equal(t, metricName, svc.evaluateRequests[0].EvaluateConfig.EvalMetrics[0].MetricName)
	}
}

func TestAgentEvaluatorEvaluateInferenceError(t *testing.T) {
	ctx := context.Background()
	svc := &fakeService{inferenceErr: errors.New("inference failed")}
	ae := &agentEvaluator{
		appName:           "app",
		runner:            stubRunner{},
		evalService:       svc,
		metricManager:     metricinmemory.New(),
		evalResultManager: evalresultinmemory.New(),
		registry:          registry.New(),
		numRuns:           1,
	}

	_, err := ae.Evaluate(ctx, "set")
	assert.Error(t, err)
}

func TestAgentEvaluatorEvaluateEmptyEvalSetID(t *testing.T) {
	ctx := context.Background()
	ae := &agentEvaluator{}

	result, err := ae.Evaluate(ctx, "")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestAgentEvaluatorRunEvaluationErrors(t *testing.T) {
	ctx := context.Background()
	appName := "app"
	evalSetID := "set"
	caseID := "case"

	baseInference := []*service.InferenceResult{{
		AppName:    appName,
		EvalSetID:  evalSetID,
		EvalCaseID: caseID,
		Inferences: []*evalset.Invocation{{InvocationID: "inv"}},
		SessionID:  "session",
		Status:     status.EvalStatusPassed,
	}}

	tests := []struct {
		name      string
		svc       *fakeService
		metricMgr metric.Manager
	}{
		{
			name:      "inference error",
			svc:       &fakeService{inferenceErr: errors.New("inference failed")},
			metricMgr: &fakeMetricManager{metrics: map[string]*metric.EvalMetric{}},
		},
		{
			name:      "metric list error",
			svc:       &fakeService{inferenceResults: [][]*service.InferenceResult{baseInference}},
			metricMgr: &fakeMetricManager{listErr: errors.New("list failed"), metrics: map[string]*metric.EvalMetric{}},
		},
		{
			name:      "metric get error",
			svc:       &fakeService{inferenceResults: [][]*service.InferenceResult{baseInference}},
			metricMgr: &fakeMetricManager{metrics: map[string]*metric.EvalMetric{"metric": {MetricName: "metric", Threshold: 1}}, getErr: errors.New("get failed")},
		},
		{
			name: "evaluate error",
			svc: &fakeService{
				inferenceResults: [][]*service.InferenceResult{baseInference},
				evaluateErr:      errors.New("evaluate failed"),
			},
			metricMgr: &fakeMetricManager{metrics: map[string]*metric.EvalMetric{"metric": {MetricName: "metric", Threshold: 1}}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ae := &agentEvaluator{
				appName:           appName,
				evalService:       tc.svc,
				metricManager:     tc.metricMgr,
				evalResultManager: evalresultinmemory.New(),
				registry:          registry.New(),
				numRuns:           1,
			}
			_, err := ae.runEvaluation(ctx, evalSetID)
			assert.Error(t, err)
		})
	}
}

func TestAggregateCaseRunsSuccess(t *testing.T) {
	caseID := "case"
	runs := []*evalresult.EvalCaseResult{
		makeEvalCaseResult("set", caseID, "metric", 0.5, 1, status.EvalStatusFailed),
		makeEvalCaseResult("set", caseID, "metric", 1.5, 1, status.EvalStatusPassed),
	}

	result, err := aggregateCaseRuns(caseID, runs)
	assert.NoError(t, err)
	assert.Equal(t, caseID, result.EvalCaseID)
	assert.Equal(t, status.EvalStatusPassed, result.OverallStatus)
	assert.Len(t, result.MetricResults, 1)
	assert.InDelta(t, 1.0, result.MetricResults[0].Score, 0.001)
	assert.Len(t, result.EvalCaseResults, 2)
}

func TestAggregateCaseRunsUnknownStatus(t *testing.T) {
	runs := []*evalresult.EvalCaseResult{
		makeEvalCaseResult("set", "case", "metric", 0.5, 1, status.EvalStatusUnknown),
	}
	result, err := aggregateCaseRuns("case", runs)
	assert.NoError(t, err)
	assert.Equal(t, status.EvalStatusFailed, result.OverallStatus)
	assert.Len(t, result.MetricResults, 1)
}

func TestAggregateCaseRunsNotEvaluated(t *testing.T) {
	runs := []*evalresult.EvalCaseResult{
		makeEvalCaseResult("set", "case", "metric", 0.5, 1, status.EvalStatusNotEvaluated),
	}
	result, err := aggregateCaseRuns("case", runs)
	assert.NoError(t, err)
	assert.Equal(t, status.EvalStatusNotEvaluated, result.OverallStatus)
	assert.Empty(t, result.MetricResults)
	assert.Len(t, result.EvalCaseResults, 1)
}

func TestSummarizeOverallStatus(t *testing.T) {
	statuses := []*EvaluationCaseResult{
		{OverallStatus: status.EvalStatusPassed},
		nil,
		{OverallStatus: status.EvalStatusNotEvaluated},
	}
	s, err := summarizeOverallStatus(statuses)
	assert.NoError(t, err)
	assert.Equal(t, status.EvalStatusPassed, s)

	statuses = []*EvaluationCaseResult{{OverallStatus: status.EvalStatusFailed}, {OverallStatus: status.EvalStatusPassed}}
	s, err = summarizeOverallStatus(statuses)
	assert.NoError(t, err)
	assert.Equal(t, status.EvalStatusFailed, s)

	statuses = []*EvaluationCaseResult{{OverallStatus: status.EvalStatusUnknown}}
	_, err = summarizeOverallStatus(statuses)
	assert.Error(t, err)

	statuses = []*EvaluationCaseResult{}
	s, err = summarizeOverallStatus(statuses)
	assert.NoError(t, err)
	assert.Equal(t, status.EvalStatusNotEvaluated, s)
}
