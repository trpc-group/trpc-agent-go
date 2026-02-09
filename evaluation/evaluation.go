//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package evaluation orchestrates agent evaluation runs and aggregates their results.
package evaluation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/multirun"
	istatus "trpc.group/trpc-go/trpc-agent-go/evaluation/internal/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// AgentEvaluator evaluates an agent against configured evaluation sets.
type AgentEvaluator interface {
	// Evaluate runs evaluation against the specified eval set.
	Evaluate(ctx context.Context, evalSetID string) (*EvaluationResult, error)
	// Close closes the evaluator and releases owned resources.
	Close() error
}

// New creates an AgentEvaluator with the supplied agent and options.
func New(appName string, runner runner.Runner, opt ...Option) (AgentEvaluator, error) {
	if runner == nil {
		return nil, errors.New("runner is nil")
	}
	opts := newOptions(opt...)
	a := &agentEvaluator{
		appName:           appName,
		runner:            runner,
		evalSetManager:    opts.evalSetManager,
		evalResultManager: opts.evalResultManager,
		metricManager:     opts.metricManager,
		registry:          opts.registry,
		evalService:       opts.evalService,
		numRuns:           opts.numRuns,
	}
	if a.numRuns <= 0 {
		return nil, errors.New("num runs must be greater than 0")
	}
	if (opts.evalCaseParallelInferenceEnabled || opts.evalCaseParallelEvaluationEnabled) && opts.evalCaseParallelism <= 0 {
		return nil, errors.New("eval case parallelism must be greater than 0")
	}
	if a.evalSetManager == nil {
		return nil, errors.New("eval set manager is nil")
	}
	if a.metricManager == nil {
		return nil, errors.New("metric manager is nil")
	}
	if a.evalResultManager == nil {
		return nil, errors.New("eval result manager is nil")
	}
	if a.evalService == nil {
		evalService, err := local.New(
			a.runner,
			service.WithEvalSetManager(a.evalSetManager),
			service.WithEvalResultManager(a.evalResultManager),
			service.WithRegistry(a.registry),
			service.WithCallbacks(opts.callbacks),
			service.WithEvalCaseParallelism(opts.evalCaseParallelism),
			service.WithEvalCaseParallelInferenceEnabled(opts.evalCaseParallelInferenceEnabled),
			service.WithEvalCaseParallelEvaluationEnabled(opts.evalCaseParallelEvaluationEnabled),
		)
		if err != nil {
			return nil, fmt.Errorf("create eval service: %w", err)
		}
		a.evalService = evalService
	}
	return a, nil
}

// agentEvaluator is the default implementation of AgentEvaluator.
type agentEvaluator struct {
	appName           string
	runner            runner.Runner
	evalSetManager    evalset.Manager
	evalResultManager evalresult.Manager
	metricManager     metric.Manager
	registry          registry.Registry
	evalService       service.Service
	numRuns           int
}

// EvaluationResult contains the aggregated outcome of running an evaluation across multiple runs.
type EvaluationResult struct {
	AppName       string                    `json:"appName"`       // AppName identifies the agent being evaluated.
	EvalSetID     string                    `json:"evalSetId"`     // EvalSetID identifies the evaluation set used in this run.
	OverallStatus status.EvalStatus         `json:"overallStatus"` // OverallStatus summarizes the aggregated evaluation status across cases.
	ExecutionTime time.Duration             `json:"executionTime"` // ExecutionTime records the total latency for the evaluation run.
	EvalCases     []*EvaluationCaseResult   `json:"evalCases"`     // EvalCases contains aggregated results for each evaluation case.
	EvalResult    *evalresult.EvalSetResult `json:"evalSetResult"` // EvalSetResult contains the aggregated results of the evaluation set.
}

// EvaluationCaseResult aggregates the outcome of a single eval case across multiple runs.
type EvaluationCaseResult struct {
	EvalCaseID      string                         `json:"evalId"`          // EvalCaseID identifies the evaluation case.
	OverallStatus   status.EvalStatus              `json:"overallStatus"`   // OverallStatus summarizes the overall status of case across runs.
	EvalCaseResults []*evalresult.EvalCaseResult   `json:"evalCaseResults"` // EvalCaseResults stores the per-run results for this case.
	MetricResults   []*evalresult.EvalMetricResult `json:"metricResults"`   // MetricResults lists aggregated metric outcomes across runs.
}

// Evaluate evaluates agent against the specified eval set across multiple runs.
func (a *agentEvaluator) Evaluate(ctx context.Context, evalSetID string) (*EvaluationResult, error) {
	if evalSetID == "" {
		return nil, errors.New("eval set id is not configured")
	}
	ctx, _ = agent.EnsureInvocation(ctx)
	start := time.Now()
	// Gather per-case results.
	evalCases, evalSetResult, err := a.collectCaseResults(ctx, evalSetID)
	if err != nil {
		return nil, fmt.Errorf("collect eval case results: %w", err)
	}
	// Reduce the case statuses to determine the overall evaluation outcome.
	status, err := summarizeOverallStatus(evalCases)
	if err != nil {
		return nil, fmt.Errorf("summarize overall status: %w", err)
	}
	return &EvaluationResult{
		AppName:       a.appName,
		EvalSetID:     evalSetID,
		OverallStatus: status,
		ExecutionTime: time.Since(start),
		EvalCases:     evalCases,
		EvalResult:    evalSetResult,
	}, nil
}

// Close closes the evaluator and releases owned resources.
func (a *agentEvaluator) Close() error {
	var overallErr error
	if a.evalService != nil {
		if err := a.evalService.Close(); err != nil {
			overallErr = errors.Join(overallErr, fmt.Errorf("close eval service: %w", err))
		}
	}
	if a.evalSetManager != nil {
		if err := a.evalSetManager.Close(); err != nil {
			overallErr = errors.Join(overallErr, fmt.Errorf("close eval set manager: %w", err))
		}
	}
	if a.metricManager != nil {
		if err := a.metricManager.Close(); err != nil {
			overallErr = errors.Join(overallErr, fmt.Errorf("close metric manager: %w", err))
		}
	}
	if a.evalResultManager != nil {
		if err := a.evalResultManager.Close(); err != nil {
			overallErr = errors.Join(overallErr, fmt.Errorf("close eval result manager: %w", err))
		}
	}
	return overallErr
}

// collectCaseResults runs evaluation on the specified eval set across multiple runs and groups results by case ID.
func (a *agentEvaluator) collectCaseResults(ctx context.Context, evalSetID string) ([]*EvaluationCaseResult, *evalresult.EvalSetResult, error) {
	// Determine eval case ordering from the eval set definition when possible.
	evalSetIndex := make(map[string]int)
	if a.evalSetManager != nil {
		evalSet, err := a.evalSetManager.Get(ctx, a.appName, evalSetID)
		if err != nil {
			return nil, nil, fmt.Errorf("get eval set: %w", err)
		}
		for i, evalCase := range evalSet.EvalCases {
			evalSetIndex[evalCase.EvalID] = i
		}
	}
	// Due to multiple runs, an evaluation case may be evaluated multiple times and generate multiple evaluation
	// case results. So EvalCaseResults need to be grouped by case ID.
	// caseResultsByID is a map from case ID to a list of eval case results.
	caseResultsByID := make(map[string][]*evalresult.EvalCaseResult)
	// Run evaluation on the specified eval set across multiple inference runs.
	evalSetResult, err := a.runEvaluation(ctx, evalSetID)
	if err != nil {
		return nil, nil, fmt.Errorf("run evaluation: %w", err)
	}
	// Group results by case ID.
	for _, caseResult := range evalSetResult.EvalCaseResults {
		caseResultsByID[caseResult.EvalID] = append(caseResultsByID[caseResult.EvalID], caseResult)
	}
	evalCaseResults := make([]*EvaluationCaseResult, 0, len(caseResultsByID))
	for caseID, runs := range caseResultsByID {
		// Aggregate multiple runs for a single case.
		evalCaseResult, err := aggregateCaseRuns(caseID, runs)
		if err != nil {
			return nil, nil, fmt.Errorf("aggregate case runs: %w", err)
		}
		evalCaseResults = append(evalCaseResults, evalCaseResult)
	}
	sort.SliceStable(evalCaseResults, func(i, j int) bool {
		leftIndex, leftOK := evalSetIndex[evalCaseResults[i].EvalCaseID]
		rightIndex, rightOK := evalSetIndex[evalCaseResults[j].EvalCaseID]
		if leftOK && rightOK {
			return leftIndex < rightIndex
		}
		if leftOK != rightOK {
			return leftOK
		}
		return evalCaseResults[i].EvalCaseID < evalCaseResults[j].EvalCaseID
	})
	return evalCaseResults, evalSetResult, nil
}

// runEvaluation runs inference and evaluation on the specified eval set.
func (a *agentEvaluator) runEvaluation(ctx context.Context, evalSetID string) (*evalresult.EvalSetResult, error) {
	// Fetch the metric configuration that will be applied to these runs.
	metricNames, err := a.metricManager.List(ctx, a.appName, evalSetID)
	if err != nil {
		return nil, fmt.Errorf("list metrics: %w", err)
	}
	evalMetrics := make([]*metric.EvalMetric, 0, len(metricNames))
	for _, metricName := range metricNames {
		metric, err := a.metricManager.Get(ctx, a.appName, evalSetID, metricName)
		if err != nil {
			return nil, fmt.Errorf("get metric %s: %w", metricName, err)
		}
		evalMetrics = append(evalMetrics, metric)
	}
	allCaseResults := make([]*evalresult.EvalCaseResult, 0)
	for runID := 1; runID <= a.numRuns; runID++ {
		inferenceRequest := &service.InferenceRequest{
			AppName:   a.appName,
			EvalSetID: evalSetID,
		}
		runInferenceResults, err := a.evalService.Inference(ctx, inferenceRequest)
		if err != nil {
			return nil, fmt.Errorf("inference: %w", err)
		}
		evaluateRequest := &service.EvaluateRequest{
			AppName:          a.appName,
			EvalSetID:        evalSetID,
			InferenceResults: runInferenceResults,
			EvaluateConfig: &service.EvaluateConfig{
				EvalMetrics: evalMetrics,
			},
		}
		runResult, err := a.evalService.Evaluate(ctx, evaluateRequest)
		if err != nil {
			return nil, fmt.Errorf("evaluate: %w", err)
		}
		if runResult == nil {
			return nil, errors.New("eval set run result is nil")
		}
		for _, caseResult := range runResult.EvalCaseResults {
			if caseResult == nil {
				continue
			}
			caseResult.RunID = runID
			allCaseResults = append(allCaseResults, caseResult)
		}
	}
	evalSetResult := &evalresult.EvalSetResult{
		EvalSetID:       evalSetID,
		EvalCaseResults: allCaseResults,
	}
	if err := multirun.SummarizeMultiRun(evalSetResult, a.numRuns); err != nil {
		return nil, fmt.Errorf("summarize eval set result: %w", err)
	}
	evalSetResultID, err := a.evalResultManager.Save(ctx, a.appName, evalSetResult)
	if err != nil {
		return nil, fmt.Errorf("save eval set result: %w", err)
	}
	evalSetResult.EvalSetResultID = evalSetResultID
	evalSetResult.EvalSetResultName = evalSetResultID
	return evalSetResult, nil
}

// aggregateCaseRuns aggregates the metric results from multiple runs of a single case.
func aggregateCaseRuns(caseID string, runs []*evalresult.EvalCaseResult) (*EvaluationCaseResult, error) {
	type aggregatedMetric struct {
		count     int
		score     float64
		threshold float64
		criterion *criterion.Criterion
	}
	hasRunError := false
	// Group metrics results by metric name.
	aggregatedMetrics := make(map[string]*aggregatedMetric)
	for _, run := range runs {
		if run == nil {
			continue
		}
		if run.ErrorMessage != "" {
			hasRunError = true
		}
		for _, metric := range run.OverallEvalMetricResults {
			// Skip metrics that did not run to avoid diluting averaged scores.
			if metric.EvalStatus == status.EvalStatusNotEvaluated {
				continue
			}
			if _, ok := aggregatedMetrics[metric.MetricName]; !ok {
				aggregatedMetrics[metric.MetricName] = &aggregatedMetric{threshold: metric.Threshold}
			}
			aggregatedMetrics[metric.MetricName].count++
			aggregatedMetrics[metric.MetricName].score += metric.Score
			aggregatedMetrics[metric.MetricName].criterion = metric.Criterion
		}
	}
	// Aggregate metrics results by metric name.
	metricResults := make([]*evalresult.EvalMetricResult, 0, len(aggregatedMetrics))
	for name, aggregatedMetric := range aggregatedMetrics {
		average := aggregatedMetric.score / float64(aggregatedMetric.count)
		evalStatus := status.EvalStatusFailed
		if average >= aggregatedMetric.threshold {
			evalStatus = status.EvalStatusPassed
		}
		metricResults = append(metricResults, &evalresult.EvalMetricResult{
			MetricName: name,
			Score:      average,
			EvalStatus: evalStatus,
			Threshold:  aggregatedMetric.threshold,
			Criterion:  aggregatedMetric.criterion,
		})
	}
	overallStatus, err := istatus.SummarizeMetricsStatus(metricResults)
	if err != nil {
		return nil, fmt.Errorf("summarize metrics status: %w", err)
	}
	if overallStatus == status.EvalStatusNotEvaluated && hasRunError {
		overallStatus = status.EvalStatusFailed
	}
	return &EvaluationCaseResult{
		EvalCaseID:      caseID,
		OverallStatus:   overallStatus,
		EvalCaseResults: runs,
		MetricResults:   metricResults,
	}, nil
}

// summarizeOverallStatus summarizes the aggregate status across all cases in the evaluation.
func summarizeOverallStatus(cases []*EvaluationCaseResult) (status.EvalStatus, error) {
	evalStatuses := make([]status.EvalStatus, 0, len(cases))
	for _, c := range cases {
		if c != nil {
			evalStatuses = append(evalStatuses, c.OverallStatus)
		}
	}
	return istatus.Summarize(evalStatuses)
}
