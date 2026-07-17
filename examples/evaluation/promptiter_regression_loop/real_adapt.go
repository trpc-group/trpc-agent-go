//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"path/filepath"

	aeval "trpc.group/trpc-go/trpc-agent-go/evaluation"
	eevalset "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiter "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

type configEvalSetLocator struct {
	paths map[string]string
}

type configMetricLocator struct {
	path string
}

func newConfigEvalSetLocator(input *LoadedInput) *configEvalSetLocator {
	return &configEvalSetLocator{
		paths: map[string]string{
			input.TrainEvalSet.EvalSetID:      resolvePath(input.ConfigDir, input.Config.TrainEvalSet),
			input.ValidationEvalSet.EvalSetID: resolvePath(input.ConfigDir, input.Config.ValidationEvalSet),
		},
	}
}

func (l *configEvalSetLocator) Build(_ string, _ string, evalSetID string) string {
	if path, ok := l.paths[evalSetID]; ok {
		return path
	}
	return filepath.Clean(evalSetID)
}

func (l *configEvalSetLocator) List(_ string, _ string) ([]string, error) {
	ids := make([]string, 0, len(l.paths))
	for id := range l.paths {
		ids = append(ids, id)
	}
	return ids, nil
}

func newConfigMetricLocator(input *LoadedInput) *configMetricLocator {
	return &configMetricLocator{path: resolvePath(input.ConfigDir, input.Config.Metrics)}
}

func (l *configMetricLocator) Build(_ string, _ string, _ string) string {
	return l.path
}

func adaptPromptIterEvaluation(
	name string,
	result *promptiterengine.EvaluationResult,
	critical map[string]bool,
) EvaluationRun {
	run := EvaluationRun{Name: name}
	if result == nil {
		return run
	}
	run.OverallScore = result.OverallScore
	for _, set := range result.EvalSets {
		run.EvalSetID = set.EvalSetID
		for _, evalCase := range set.Cases {
			metrics := make([]MetricResult, 0, len(evalCase.Metrics))
			score := 0.0
			caseStatus := status.EvalStatusPassed
			for _, metric := range evalCase.Metrics {
				metricResult := MetricResult{
					MetricName: metric.MetricName,
					Score:      metric.Score,
					Status:     metric.Status,
					Reason:     metric.Reason,
				}
				metrics = append(metrics, metricResult)
				score += metric.Score
				if metric.Status != status.EvalStatusPassed {
					caseStatus = status.EvalStatusFailed
				}
			}
			if len(metrics) > 0 {
				score = score / float64(len(metrics))
			}
			caseResult := CaseResult{
				EvalSetID:      set.EvalSetID,
				CaseID:         evalCase.EvalCaseID,
				Critical:       critical[evalCase.EvalCaseID],
				Score:          score,
				Status:         caseStatus,
				Metrics:        metrics,
				FailureReasons: AttributeFailures(metrics, Invocation{}, Invocation{}),
				Trace: TraceSummary{
					Mode: "real_trace",
				},
			}
			if evalCase.Trace != nil {
				caseResult.Trace.Signals = []string{"execution_trace_recorded"}
				caseResult.Trace.Route = "agent_trace"
			}
			run.Cases = append(run.Cases, caseResult)
			if caseStatus == status.EvalStatusPassed {
				run.Passed++
			} else {
				run.Failed++
			}
		}
	}
	return run
}

func adaptEvaluationResult(
	name string,
	result *aeval.EvaluationResult,
	critical map[string]bool,
) EvaluationRun {
	run := EvaluationRun{Name: name}
	if result == nil {
		return run
	}
	run.EvalSetID = result.EvalSetID
	run.LatencyMs = result.ExecutionTime.Milliseconds()
	total := 0.0
	for _, evalCase := range result.EvalCases {
		caseResult, ok := adaptEvaluationCaseResult(result.EvalSetID, evalCase, critical)
		if !ok {
			continue
		}
		total += caseResult.Score
		run.Cases = append(run.Cases, caseResult)
		if caseResult.Status == status.EvalStatusPassed {
			run.Passed++
		} else {
			run.Failed++
		}
	}
	if len(run.Cases) > 0 {
		run.OverallScore = total / float64(len(run.Cases))
	}
	return run
}

func adaptEvaluationCaseResult(
	evalSetID string,
	evalCase *aeval.EvaluationCaseResult,
	critical map[string]bool,
) (CaseResult, bool) {
	if evalCase == nil {
		return CaseResult{}, false
	}
	metrics, score, caseStatus := adaptEvaluationMetrics(evalCase)
	caseResult := CaseResult{
		EvalSetID:      evalSetID,
		CaseID:         evalCase.EvalCaseID,
		Critical:       critical[evalCase.EvalCaseID],
		Score:          score,
		Status:         caseStatus,
		Metrics:        metrics,
		FailureReasons: AttributeFailures(metrics, Invocation{}, Invocation{}),
		Trace:          TraceSummary{Mode: "real_trace"},
	}
	applyRunDetails(&caseResult, evalCase)
	applyInvocationDetails(&caseResult, evalCase)
	return caseResult, true
}

func adaptEvaluationMetrics(evalCase *aeval.EvaluationCaseResult) ([]MetricResult, float64, status.EvalStatus) {
	metrics := make([]MetricResult, 0, len(evalCase.MetricResults))
	score := 0.0
	caseStatus := status.EvalStatusPassed
	for _, metric := range evalCase.MetricResults {
		if metric == nil {
			continue
		}
		reason := ""
		if metric.Details != nil {
			reason = metric.Details.Reason
		}
		metrics = append(metrics, MetricResult{
			MetricName: metric.MetricName,
			Score:      metric.Score,
			Threshold:  metric.Threshold,
			Status:     metric.EvalStatus,
			Reason:     reason,
		})
		score += metric.Score
		if metric.EvalStatus != status.EvalStatusPassed {
			caseStatus = status.EvalStatusFailed
		}
	}
	if len(metrics) > 0 {
		score = score / float64(len(metrics))
	}
	return metrics, score, caseStatus
}

func applyRunDetails(caseResult *CaseResult, evalCase *aeval.EvaluationCaseResult) {
	if len(evalCase.RunDetails) == 0 || evalCase.RunDetails[0] == nil || evalCase.RunDetails[0].Inference == nil {
		return
	}
	inference := evalCase.RunDetails[0].Inference
	if len(inference.Inferences) > 0 {
		caseResult.Actual = convertInvocation(inference.Inferences[0])
	}
	if len(inference.ExecutionTraces) > 0 {
		caseResult.Trace.Route = "agent_trace"
		caseResult.Trace.Signals = []string{"execution_trace_recorded"}
	}
}

func applyInvocationDetails(caseResult *CaseResult, evalCase *aeval.EvaluationCaseResult) {
	if len(evalCase.EvalCaseResults) == 0 || evalCase.EvalCaseResults[0] == nil {
		return
	}
	perInvocationResults := evalCase.EvalCaseResults[0].EvalMetricResultPerInvocation
	if len(perInvocationResults) == 0 || perInvocationResults[0] == nil {
		return
	}
	perInvocation := perInvocationResults[0]
	caseResult.Expected = convertInvocation(perInvocation.ExpectedInvocation)
	if perInvocation.ActualInvocation != nil {
		caseResult.Actual = convertInvocation(perInvocation.ActualInvocation)
	}
}

func convertInvocation(invocation *eevalset.Invocation) Invocation {
	if invocation == nil {
		return Invocation{}
	}
	result := Invocation{
		InvocationID: invocation.InvocationID,
		Tools:        make([]ToolCall, 0, len(invocation.Tools)),
	}
	if invocation.UserContent != nil {
		result.UserContent = &Message{Role: string(invocation.UserContent.Role), Content: invocation.UserContent.Content}
	}
	if invocation.FinalResponse != nil {
		result.FinalResponse = &Message{Role: string(invocation.FinalResponse.Role), Content: invocation.FinalResponse.Content}
	}
	for _, tool := range invocation.Tools {
		if tool == nil {
			continue
		}
		result.Tools = append(result.Tools, ToolCall{
			ID:        tool.ID,
			Name:      tool.Name,
			Arguments: tool.Arguments,
			Result:    tool.Result,
		})
	}
	return result
}

func criticalCaseSet(configured []string, validation EvalSetInput) map[string]bool {
	critical := make(map[string]bool)
	for _, caseID := range configured {
		critical[caseID] = true
	}
	for _, evalCase := range validation.Cases {
		if evalCase.Critical {
			critical[evalCase.EvalID] = true
		}
	}
	return critical
}

func promptFromProfile(fallback string, profile *promptiter.Profile) string {
	if profile == nil {
		return fallback
	}
	for _, override := range profile.Overrides {
		if override.Value.Text != nil && *override.Value.Text != "" {
			return *override.Value.Text
		}
	}
	return fallback
}

func estimateRealCost(runs ...EvaluationRun) CostSummary {
	var total CostSummary
	for _, run := range runs {
		for _, evalCase := range run.Cases {
			total.TotalCalls++
			total.PromptTokens += estimateTokens(messageText(evalCase.Expected.UserContent))
			total.CompletionTokens += estimateTokens(messageText(evalCase.Actual.FinalResponse))
		}
	}
	total.EstimatedUSD = float64(total.PromptTokens+total.CompletionTokens) * 0.0000005
	return total
}
