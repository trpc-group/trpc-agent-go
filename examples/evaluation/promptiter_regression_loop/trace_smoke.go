//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type traceSmokeRuntime struct {
	evaluator evaluation.AgentEvaluator
	runner    runner.Runner
	model     *fakeModel
}

func runTraceSmokePipeline(ctx context.Context, cfg RunConfig) (*PipelineResult, error) {
	if strings.TrimSpace(cfg.Mode) == "" {
		cfg.Mode = traceSmokeMode
	}
	if cfg.Mode != traceSmokeMode {
		return nil, fmt.Errorf("run trace smoke pipeline: mode must be %q, got %q", traceSmokeMode, cfg.Mode)
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./output"
	}
	cfg.DataDir = resolveExamplePath(cfg.DataDir)
	cfg.OutputDir = resolveExamplePath(cfg.OutputDir)

	runtime, err := buildTraceSmokeRuntime(cfg)
	if err != nil {
		return nil, err
	}
	defer runtime.close()

	evaluationStart := time.Now()
	agentResult, err := runtime.evaluator.Evaluate(
		ctx,
		traceSmokeEvalSetID,
		evaluation.WithRunDetailsEnabled(true),
		evaluation.WithNumRuns(1),
	)
	if err != nil {
		return nil, fmt.Errorf("evaluate trace smoke: %w", err)
	}
	latencyMs := reportLatencyMs(time.Since(evaluationStart), cfg.SampleReport)
	observations := runtime.model.observations()
	if observations.RequestCount != 0 {
		return nil, fmt.Errorf("trace smoke unexpectedly invoked model %d time(s)", observations.RequestCount)
	}
	engineResult, err := adaptAgentEvaluationResultToEngine(agentResult)
	if err != nil {
		return nil, err
	}
	attribution, err := buildFailureAttribution(engineResult)
	if err != nil {
		return nil, err
	}
	report := newTraceSmokeOptimizationReport(
		evaluationSummary(engineResult),
		attribution,
		ReportContext{
			Mode:           cfg.Mode,
			Seed:           deterministicSeed,
			ModelConfig:    fakeModelConfigSummary(),
			LatencyMs:      latencyMs,
			ModelCallCount: observations.RequestCount,
		},
	)
	jsonPath, markdownPath, err := writeOptimizationReport(cfg.OutputDir, report)
	if err != nil {
		return nil, err
	}
	return &PipelineResult{
		Report:             report,
		ModelObservations:  observations,
		ReportJSONPath:     jsonPath,
		ReportMarkdownPath: markdownPath,
	}, nil
}

func buildTraceSmokeRuntime(cfg RunConfig) (*traceSmokeRuntime, error) {
	candidateModel := newFakeModel()
	candidateAgent, err := newCandidateAgent(
		candidateModel,
		"Trace smoke mode replays recorded invocations; this agent should not be called.",
		initialToolDescription,
	)
	if err != nil {
		return nil, err
	}
	candidateRunner := runner.NewRunner(candidateRunnerAppName, candidateAgent)
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir))
	metricManager := metriclocal.New(
		metric.WithBaseDir(cfg.DataDir),
		metric.WithLocator(sharedMetricLocator{}),
	)
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(cfg.OutputDir))
	agentEvaluator, err := evaluation.New(
		appName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(registry.New()),
	)
	if err != nil {
		candidateRunner.Close()
		return nil, fmt.Errorf("create trace smoke evaluator: %w", err)
	}
	return &traceSmokeRuntime{
		evaluator: agentEvaluator,
		runner:    candidateRunner,
		model:     candidateModel,
	}, nil
}

func (r *traceSmokeRuntime) close() {
	if r == nil {
		return
	}
	if r.evaluator != nil {
		_ = r.evaluator.Close()
	}
	if r.runner != nil {
		r.runner.Close()
	}
}

func adaptAgentEvaluationResultToEngine(result *evaluation.EvaluationResult) (*promptiterengine.EvaluationResult, error) {
	if result == nil {
		return nil, errors.New("evaluation result is nil")
	}
	if strings.TrimSpace(result.EvalSetID) == "" {
		return nil, errors.New("evaluation result eval set id is empty")
	}
	evalSetResult := promptiterengine.EvalSetResult{
		EvalSetID: result.EvalSetID,
		Cases:     make([]promptiterengine.CaseResult, 0, len(result.EvalCases)),
	}
	totalScore := 0.0
	totalMetrics := 0
	for _, evalCase := range result.EvalCases {
		if evalCase == nil {
			continue
		}
		runResult := firstEvalCaseRunResult(evalCase)
		metrics := engineMetricResults(metricResultsForCase(evalCase, runResult))
		if len(metrics) == 0 {
			return nil, fmt.Errorf("evaluation case %q has no metric scores", evalCase.EvalCaseID)
		}
		for _, metricResult := range metrics {
			totalScore += metricResult.Score
			totalMetrics++
		}
		evalSetResult.Cases = append(evalSetResult.Cases, promptiterengine.CaseResult{
			EvalSetID:  result.EvalSetID,
			EvalCaseID: evalCase.EvalCaseID,
			SessionID:  sessionIDForCase(evalCase, runResult),
			Trace:      traceForCase(evalCase, runResult),
			Metrics:    metrics,
		})
	}
	if totalMetrics == 0 {
		return nil, errors.New("evaluation result has no metric scores")
	}
	evalSetResult.OverallScore = totalScore / float64(totalMetrics)
	return &promptiterengine.EvaluationResult{
		OverallScore: evalSetResult.OverallScore,
		EvalSets:     []promptiterengine.EvalSetResult{evalSetResult},
	}, nil
}

func firstEvalCaseRunResult(evalCase *evaluation.EvaluationCaseResult) *evalresult.EvalCaseResult {
	if evalCase == nil {
		return nil
	}
	for _, runResult := range evalCase.EvalCaseResults {
		if runResult != nil {
			return runResult
		}
	}
	return nil
}

func metricResultsForCase(
	evalCase *evaluation.EvaluationCaseResult,
	runResult *evalresult.EvalCaseResult,
) []*evalresult.EvalMetricResult {
	if runResult != nil && len(runResult.OverallEvalMetricResults) > 0 {
		return runResult.OverallEvalMetricResults
	}
	if evalCase != nil {
		return evalCase.MetricResults
	}
	return nil
}

func engineMetricResults(results []*evalresult.EvalMetricResult) []promptiterengine.MetricResult {
	metrics := make([]promptiterengine.MetricResult, 0, len(results))
	for _, result := range results {
		if result == nil || result.EvalStatus == status.EvalStatusNotEvaluated {
			continue
		}
		metricResult := promptiterengine.MetricResult{
			MetricName: result.MetricName,
			Score:      result.Score,
			Status:     result.EvalStatus,
		}
		if result.Details != nil {
			metricResult.Reason = strings.TrimSpace(result.Details.Reason)
		}
		metrics = append(metrics, metricResult)
	}
	return metrics
}

func sessionIDForCase(
	evalCase *evaluation.EvaluationCaseResult,
	runResult *evalresult.EvalCaseResult,
) string {
	for _, detail := range evalCase.RunDetails {
		if detail != nil && detail.Inference != nil && detail.Inference.SessionID != "" {
			return detail.Inference.SessionID
		}
	}
	if runResult != nil {
		return runResult.SessionID
	}
	return ""
}

func traceForCase(
	evalCase *evaluation.EvaluationCaseResult,
	runResult *evalresult.EvalCaseResult,
) *trace.Trace {
	for _, detail := range evalCase.RunDetails {
		if detail == nil || detail.Inference == nil {
			continue
		}
		if len(detail.Inference.ExecutionTraces) > 0 && detail.Inference.ExecutionTraces[0] != nil {
			return detail.Inference.ExecutionTraces[0]
		}
		for _, invocation := range detail.Inference.Inferences {
			if invocation != nil && invocation.ExecutionTrace != nil {
				return invocation.ExecutionTrace
			}
		}
	}
	if runResult == nil {
		return nil
	}
	for _, perInvocation := range runResult.EvalMetricResultPerInvocation {
		if perInvocation != nil &&
			perInvocation.ActualInvocation != nil &&
			perInvocation.ActualInvocation.ExecutionTrace != nil {
			return perInvocation.ActualInvocation.ExecutionTrace
		}
	}
	return nil
}
