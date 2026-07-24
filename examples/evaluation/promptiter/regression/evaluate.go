//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	agenttrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type evaluationRuntime struct {
	evaluator evaluation.AgentEvaluator
	model     fakeModelConfig
	evalSets  map[string]*evalset.EvalSet
}

func loadEvaluationInputs(
	config pipelineConfig,
) (*evalset.EvalSet, *evalset.EvalSet, []*metric.EvalMetric, string, error) {
	train, err := loadJSONFile[evalset.EvalSet](config.Inputs.TrainEvalSet)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("load train eval set: %w", err)
	}
	validation, err := loadJSONFile[evalset.EvalSet](config.Inputs.ValidationEvalSet)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("load validation eval set: %w", err)
	}
	metrics, err := loadJSONFile[[]*metric.EvalMetric](config.Inputs.Metrics)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("load metrics: %w", err)
	}
	promptData, err := os.ReadFile(config.Inputs.PromptSource)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("read prompt source: %w", err)
	}
	prompt := strings.TrimSpace(string(promptData))
	if prompt == "" {
		return nil, nil, nil, "", errors.New("baseline prompt is empty")
	}
	if err := validateEvaluationInputs(train, validation, *metrics); err != nil {
		return nil, nil, nil, "", err
	}
	return train, validation, *metrics, prompt, nil
}

func loadJSONFile[T any](path string) (*T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return &value, nil
}

func validateEvaluationInputs(
	train *evalset.EvalSet,
	validation *evalset.EvalSet,
	metrics []*metric.EvalMetric,
) error {
	switch {
	case train == nil || strings.TrimSpace(train.EvalSetID) == "":
		return errors.New("train eval set id is empty")
	case validation == nil || strings.TrimSpace(validation.EvalSetID) == "":
		return errors.New("validation eval set id is empty")
	case train.EvalSetID == validation.EvalSetID:
		return errors.New("train and validation eval set ids must differ")
	case len(train.EvalCases) == 0:
		return errors.New("train eval set has no cases")
	case len(validation.EvalCases) == 0:
		return errors.New("validation eval set has no cases")
	case len(metrics) == 0:
		return errors.New("metrics list is empty")
	}
	return nil
}

func newEvaluationRuntime(
	ctx context.Context,
	config pipelineConfig,
	train *evalset.EvalSet,
	validation *evalset.EvalSet,
	metrics []*metric.EvalMetric,
) (*evaluationRuntime, error) {
	evalSetManager := evalsetinmemory.New()
	metricManager := metricinmemory.New()
	resultManager := evalresultinmemory.New()
	for _, evalSet := range []*evalset.EvalSet{train, validation} {
		if err := addEvalSet(ctx, evalSetManager, config.AppName, evalSet); err != nil {
			return nil, err
		}
		for _, evalMetric := range metrics {
			if err := metricManager.Add(ctx, config.AppName, evalSet.EvalSetID, evalMetric); err != nil {
				return nil, fmt.Errorf(
					"add metric %q to eval set %q: %w",
					evalMetric.MetricName,
					evalSet.EvalSetID,
					err,
				)
			}
		}
	}
	runner := &deterministicRunner{
		targetSurfaceID: config.TargetSurfaceID,
		model:           config.FakeModel,
		seed:            config.Seed,
	}
	agentEvaluator, err := evaluation.New(
		config.AppName,
		runner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(resultManager),
		evaluation.WithNumRuns(1),
	)
	if err != nil {
		return nil, fmt.Errorf("create agent evaluator: %w", err)
	}
	return &evaluationRuntime{
		evaluator: agentEvaluator,
		model:     config.FakeModel,
		evalSets: map[string]*evalset.EvalSet{
			train.EvalSetID:      train,
			validation.EvalSetID: validation,
		},
	}, nil
}

func addEvalSet(
	ctx context.Context,
	manager evalset.Manager,
	appName string,
	input *evalset.EvalSet,
) error {
	if _, err := manager.Create(ctx, appName, input.EvalSetID); err != nil {
		return fmt.Errorf("create eval set %q: %w", input.EvalSetID, err)
	}
	for _, evalCase := range input.EvalCases {
		if err := manager.AddCase(ctx, appName, input.EvalSetID, evalCase); err != nil {
			return fmt.Errorf("add case to eval set %q: %w", input.EvalSetID, err)
		}
	}
	return nil
}

func (r *evaluationRuntime) close() error {
	if r == nil || r.evaluator == nil {
		return nil
	}
	return r.evaluator.Close()
}

func (r *evaluationRuntime) evaluate(
	ctx context.Context,
	evalSetID string,
	prompt string,
) (evaluationSummary, error) {
	expectedSet, ok := r.evalSets[evalSetID]
	if !ok {
		return evaluationSummary{}, fmt.Errorf("eval set %q is not loaded", evalSetID)
	}
	result, err := r.evaluator.Evaluate(
		ctx,
		evalSetID,
		evaluation.WithRunDetailsEnabled(true),
		evaluation.WithRunOptions(agent.WithInstruction(prompt)),
	)
	if err != nil {
		return evaluationSummary{}, fmt.Errorf("evaluate %q: %w", evalSetID, err)
	}
	return r.adaptEvaluation(result, expectedSet)
}

func (r *evaluationRuntime) adaptEvaluation(
	result *evaluation.EvaluationResult,
	expectedSet *evalset.EvalSet,
) (evaluationSummary, error) {
	if result == nil {
		return evaluationSummary{}, errors.New("evaluation result is nil")
	}
	expectedCases := make(map[string]*evalset.EvalCase, len(expectedSet.EvalCases))
	for _, evalCase := range expectedSet.EvalCases {
		if evalCase != nil {
			expectedCases[evalCase.EvalID] = evalCase
		}
	}
	summary := evaluationSummary{
		EvalSetID: result.EvalSetID,
		Cases:     make([]caseEvaluation, 0, len(result.EvalCases)),
	}
	totalMetricScore := 0.0
	metricCount := 0
	for _, evalCase := range result.EvalCases {
		if evalCase == nil {
			continue
		}
		expected, ok := expectedCases[evalCase.EvalCaseID]
		if !ok {
			return evaluationSummary{}, fmt.Errorf(
				"expected eval case %q is missing",
				evalCase.EvalCaseID,
			)
		}
		adapted, err := r.adaptCase(evalCase, expected)
		if err != nil {
			return evaluationSummary{}, fmt.Errorf("adapt case %q: %w", evalCase.EvalCaseID, err)
		}
		for _, metricResult := range adapted.Metrics {
			totalMetricScore += metricResult.Score
			metricCount++
		}
		if adapted.Passed {
			summary.PassedCases++
		} else {
			summary.FailedCases++
		}
		addCost(&summary.Cost, adapted.Cost)
		summary.LatencyMillis += adapted.LatencyMillis
		summary.Cases = append(summary.Cases, adapted)
	}
	if metricCount == 0 {
		return evaluationSummary{}, errors.New("evaluation result contains no metric scores")
	}
	summary.Score = roundScore(totalMetricScore / float64(metricCount))
	sort.Slice(summary.Cases, func(i, j int) bool {
		return summary.Cases[i].CaseID < summary.Cases[j].CaseID
	})
	return summary, nil
}

func (r *evaluationRuntime) adaptCase(
	result *evaluation.EvaluationCaseResult,
	expected *evalset.EvalCase,
) (caseEvaluation, error) {
	if len(result.RunDetails) != 1 || result.RunDetails[0] == nil ||
		result.RunDetails[0].Inference == nil {
		return caseEvaluation{}, errors.New("case must contain exactly one inference run detail")
	}
	inference := result.RunDetails[0].Inference
	actualInvocations := inference.Inferences
	expectedInvocations := expected.Conversation
	actualResponse := lastResponse(actualInvocations)
	expectedResponse := lastResponse(expectedInvocations)
	actualTools := flattenTools(actualInvocations)
	expectedTools := flattenTools(expectedInvocations)
	metricResults := result.MetricResults
	if len(result.EvalCaseResults) == 1 && result.EvalCaseResults[0] != nil {
		metricResults = result.EvalCaseResults[0].OverallEvalMetricResults
	}
	metrics := adaptMetrics(metricResults)
	adapted := caseEvaluation{
		CaseID:                 result.EvalCaseID,
		Score:                  averageMetricScore(metrics),
		Passed:                 allMetricsPassed(metrics),
		FinalResponse:          actualResponse,
		ExpectedResponse:       expectedResponse,
		ToolTrajectory:         actualTools,
		ExpectedToolTrajectory: expectedTools,
		Metrics:                metrics,
	}
	adapted.Trace, adapted.Cost, adapted.LatencyMillis = r.adaptTraces(inference.ExecutionTraces)
	adapted.Cost.ToolCalls = len(actualTools)
	if !adapted.Passed {
		adapted.FailureAttributions = attributeFailures(attributionInput{
			metrics:          metrics,
			actualResponse:   actualResponse,
			expectedResponse: expectedResponse,
			actualTools:      actualTools,
			expectedTools:    expectedTools,
			trace:            adapted.Trace,
		})
	}
	return adapted, nil
}

func adaptMetrics(results []*evalresult.EvalMetricResult) []metricEvaluation {
	metrics := make([]metricEvaluation, 0, len(results))
	for _, result := range results {
		if result == nil || result.EvalStatus == status.EvalStatusNotEvaluated {
			continue
		}
		adapted := metricEvaluation{
			Name:      result.MetricName,
			Score:     result.Score,
			Threshold: result.Threshold,
			Passed:    result.EvalStatus == status.EvalStatusPassed,
		}
		if result.Details != nil {
			adapted.Reason = strings.TrimSpace(result.Details.Reason)
		}
		metrics = append(metrics, adapted)
	}
	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].Name < metrics[j].Name
	})
	return metrics
}

func lastResponse(invocations []*evalset.Invocation) string {
	for index := len(invocations) - 1; index >= 0; index-- {
		if invocations[index] != nil && invocations[index].FinalResponse != nil {
			return invocations[index].FinalResponse.Content
		}
	}
	return ""
}

func flattenTools(invocations []*evalset.Invocation) []toolAudit {
	tools := make([]toolAudit, 0)
	for _, invocation := range invocations {
		if invocation == nil {
			continue
		}
		for _, toolCall := range invocation.Tools {
			if toolCall == nil {
				continue
			}
			tools = append(tools, toolAudit{
				ID:        toolCall.ID,
				Name:      toolCall.Name,
				Arguments: toolCall.Arguments,
				Result:    toolCall.Result,
			})
		}
	}
	return tools
}

func (r *evaluationRuntime) adaptTraces(
	traces []*agenttrace.Trace,
) (traceAudit, costSummary, int64) {
	audit := traceAudit{Steps: make([]traceStepAudit, 0)}
	var cost costSummary
	var latency int64
	for _, trace := range traces {
		if trace == nil {
			continue
		}
		audit.Status = string(trace.Status)
		if trace.Usage != nil {
			cost.ModelCalls++
			cost.PromptTokens += trace.Usage.PromptTokens
			cost.CompletionTokens += trace.Usage.CompletionTokens
			cost.TotalTokens += trace.Usage.TotalTokens
		}
		latency += trace.EndedAt.Sub(trace.StartedAt).Milliseconds()
		for _, step := range trace.Steps {
			audit.Steps = append(audit.Steps, traceStepAudit{
				StepID:            step.StepID,
				NodeID:            step.NodeID,
				NodeType:          step.NodeType,
				AppliedSurfaceIDs: append([]string(nil), step.AppliedSurfaceIDs...),
				Error:             step.Error,
			})
		}
	}
	cost.EstimatedCostUSD = estimateCost(cost, r.model)
	return audit, cost, latency
}

func averageMetricScore(metrics []metricEvaluation) float64 {
	if len(metrics) == 0 {
		return 0
	}
	total := 0.0
	for _, metric := range metrics {
		total += metric.Score
	}
	return roundScore(total / float64(len(metrics)))
}

func allMetricsPassed(metrics []metricEvaluation) bool {
	if len(metrics) == 0 {
		return false
	}
	for _, metric := range metrics {
		if !metric.Passed {
			return false
		}
	}
	return true
}

func estimateCost(cost costSummary, model fakeModelConfig) float64 {
	value := float64(cost.PromptTokens)*model.PromptCostPerMillionTokens/1_000_000 +
		float64(cost.CompletionTokens)*model.OutputCostPerMillionTokens/1_000_000
	return roundCost(value)
}

func roundCost(value float64) float64 {
	const precision = 1_000_000_000
	return float64(int64(value*precision+0.5)) / precision
}

func addCost(target *costSummary, addition costSummary) {
	target.ModelCalls += addition.ModelCalls
	target.ToolCalls += addition.ToolCalls
	target.PromptTokens += addition.PromptTokens
	target.CompletionTokens += addition.CompletionTokens
	target.TotalTokens += addition.TotalTokens
	target.EstimatedCostUSD = roundCost(target.EstimatedCostUSD + addition.EstimatedCostUSD)
}
