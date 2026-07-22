// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// NormalizeAgentEvaluation converts the public Evaluation result into the
// stable, strictly validated representation consumed by the release gate.
func NormalizeAgentEvaluation(result *evaluation.EvaluationResult) (*EvaluationResult, error) {
	if result == nil {
		return nil, errors.New("evaluation result is nil")
	}
	if strings.TrimSpace(result.EvalSetID) == "" {
		return nil, errors.New("evaluation result eval set id is empty")
	}
	if len(result.EvalCases) == 0 {
		return nil, errors.New("evaluation result has no cases")
	}
	normalized := &EvaluationResult{
		EvalSetID:     result.EvalSetID,
		OverallStatus: result.OverallStatus,
		ExecutionTime: result.ExecutionTime,
		Cases:         make([]CaseResult, 0, len(result.EvalCases)),
		Usage:         Usage{Measured: true},
	}
	seenCases := make(map[string]struct{}, len(result.EvalCases))
	totalScore := 0.0
	totalMetrics := 0
	for _, evalCase := range result.EvalCases {
		caseResult, evaluatedMetrics, err := normalizeAgentCase(result.EvalSetID, evalCase)
		if err != nil {
			return nil, err
		}
		if _, ok := seenCases[caseResult.CaseID]; ok {
			return nil, fmt.Errorf("duplicate evaluation case %q", caseResult.CaseID)
		}
		seenCases[caseResult.CaseID] = struct{}{}
		normalized.Cases = append(normalized.Cases, caseResult)
		for _, metricResult := range caseResult.Metrics {
			if metricResult.Status == status.EvalStatusNotEvaluated {
				continue
			}
			totalScore += metricResult.Score
		}
		totalMetrics += evaluatedMetrics
		normalized.Usage = AddUsage(normalized.Usage, caseResult.Trace.Usage)
	}
	if totalMetrics > 0 {
		normalized.OverallScore = totalScore / float64(totalMetrics)
	}
	if !finite(normalized.OverallScore) {
		return nil, errors.New("evaluation overall score is not finite")
	}
	normalized.Usage.Duration = result.ExecutionTime
	sort.Slice(normalized.Cases, func(i, j int) bool {
		return normalized.Cases[i].CaseID < normalized.Cases[j].CaseID
	})
	return normalized, nil
}

func normalizeAgentCase(
	evalSetID string,
	evalCase *evaluation.EvaluationCaseResult,
) (CaseResult, int, error) {
	if evalCase == nil {
		return CaseResult{}, 0, errors.New("evaluation case is nil")
	}
	if strings.TrimSpace(evalCase.EvalCaseID) == "" {
		return CaseResult{}, 0, errors.New("evaluation case id is empty")
	}
	result := CaseResult{
		EvalSetID: evalSetID,
		CaseID:    evalCase.EvalCaseID,
		Metrics:   make([]MetricResult, 0, len(evalCase.MetricResults)),
		Passed:    evalCase.OverallStatus == status.EvalStatusPassed,
	}
	runResult := firstRunResult(evalCase.EvalCaseResults)
	if runResult != nil {
		result.ErrorMessage = runResult.ErrorMessage
	}
	metrics := evalCase.MetricResults
	if len(metrics) == 0 && runResult != nil {
		metrics = runResult.OverallEvalMetricResults
	}
	seenMetrics := make(map[string]struct{}, len(metrics))
	evaluated := 0
	for _, metricResult := range metrics {
		if metricResult == nil {
			return CaseResult{}, 0, fmt.Errorf("evaluation case %q has a nil metric", result.CaseID)
		}
		name := strings.TrimSpace(metricResult.MetricName)
		if name == "" {
			return CaseResult{}, 0, fmt.Errorf("evaluation case %q has an empty metric name", result.CaseID)
		}
		if _, ok := seenMetrics[name]; ok {
			return CaseResult{}, 0, fmt.Errorf("evaluation case %q has duplicate metric %q", result.CaseID, name)
		}
		seenMetrics[name] = struct{}{}
		if !finite(metricResult.Score) || !finite(metricResult.Threshold) {
			return CaseResult{}, 0, fmt.Errorf("evaluation case %q metric %q has a non-finite value", result.CaseID, name)
		}
		reason := metricReason(metricResult)
		if reason == "" && runResult != nil {
			reason = runMetricReason(runResult, name)
		}
		result.Metrics = append(result.Metrics, MetricResult{
			Name: name, Score: metricResult.Score, Threshold: metricResult.Threshold,
			Status: metricResult.EvalStatus, Reason: reason,
		})
		if metricResult.EvalStatus != status.EvalStatusNotEvaluated {
			result.Score += metricResult.Score
			evaluated++
		}
	}
	if len(result.Metrics) == 0 {
		if result.ErrorMessage == "" {
			return CaseResult{}, 0, fmt.Errorf("evaluation case %q has no metrics", result.CaseID)
		}
		result.Metrics = []MetricResult{{
			Name: "execution", Score: 0, Status: status.EvalStatusFailed,
			Reason: result.ErrorMessage,
		}}
		evaluated = 1
	}
	if evaluated > 0 {
		result.Score /= float64(evaluated)
	}
	trace := firstTrace(evalCase.RunDetails)
	result.Trace = normalizeTrace(trace)
	if result.ErrorMessage != "" {
		result.Passed = false
	}
	sort.Slice(result.Metrics, func(i, j int) bool {
		return result.Metrics[i].Name < result.Metrics[j].Name
	})
	return result, evaluated, nil
}

func firstRunResult(results []*evalresult.EvalCaseResult) *evalresult.EvalCaseResult {
	for _, result := range results {
		if result != nil {
			return result
		}
	}
	return nil
}

func metricReason(result *evalresult.EvalMetricResult) string {
	if result == nil || result.Details == nil {
		return ""
	}
	if reason := strings.TrimSpace(result.Details.Reason); reason != "" {
		return reason
	}
	for _, rubric := range result.Details.RubricScores {
		if rubric != nil && strings.TrimSpace(rubric.Reason) != "" {
			return strings.TrimSpace(rubric.Reason)
		}
	}
	return ""
}

func runMetricReason(result *evalresult.EvalCaseResult, name string) string {
	if result == nil {
		return ""
	}
	for _, metricResult := range result.OverallEvalMetricResults {
		if metricResult != nil && metricResult.MetricName == name {
			return metricReason(metricResult)
		}
	}
	for _, invocation := range result.EvalMetricResultPerInvocation {
		if invocation == nil {
			continue
		}
		for _, metricResult := range invocation.EvalMetricResults {
			if metricResult != nil && metricResult.MetricName == name {
				if reason := metricReason(metricResult); reason != "" {
					return reason
				}
			}
		}
	}
	return ""
}

func firstTrace(details []*evaluation.EvaluationCaseRunDetails) *atrace.Trace {
	for _, detail := range details {
		if detail == nil || detail.Inference == nil {
			continue
		}
		for _, executionTrace := range detail.Inference.ExecutionTraces {
			if executionTrace != nil {
				return executionTrace
			}
		}
		for _, invocation := range detail.Inference.Inferences {
			if invocation != nil && invocation.ExecutionTrace != nil {
				return invocation.ExecutionTrace
			}
		}
	}
	return nil
}

// NormalizeEngineEvaluation converts a PromptIter engine result for internal
// usage accounting and consistency checks.
func NormalizeEngineEvaluation(result *promptiterengine.EvaluationResult) (*EvaluationResult, error) {
	if result == nil {
		return nil, errors.New("PromptIter evaluation result is nil")
	}
	if len(result.EvalSets) == 0 {
		return nil, errors.New("PromptIter evaluation result has no eval sets")
	}
	normalized := &EvaluationResult{
		OverallScore:  result.OverallScore,
		OverallStatus: status.EvalStatusPassed,
		Cases:         make([]CaseResult, 0),
		Usage:         Usage{Measured: true},
	}
	if !finite(result.OverallScore) {
		return nil, errors.New("PromptIter evaluation score is not finite")
	}
	for _, evalSet := range result.EvalSets {
		if normalized.EvalSetID == "" {
			normalized.EvalSetID = evalSet.EvalSetID
		}
		for _, evalCase := range evalSet.Cases {
			caseResult := CaseResult{
				EvalSetID: evalSet.EvalSetID, CaseID: evalCase.EvalCaseID,
				Metrics: make([]MetricResult, 0, len(evalCase.Metrics)), Passed: true,
				Trace: normalizeTrace(evalCase.Trace),
			}
			for _, metricResult := range evalCase.Metrics {
				if !finite(metricResult.Score) {
					return nil, fmt.Errorf("PromptIter metric %q score is not finite", metricResult.MetricName)
				}
				caseResult.Metrics = append(caseResult.Metrics, MetricResult{
					Name: metricResult.MetricName, Score: metricResult.Score,
					Status: metricResult.Status, Reason: metricResult.Reason,
				})
				caseResult.Score += metricResult.Score
				if metricResult.Status != status.EvalStatusPassed {
					caseResult.Passed = false
				}
				switch metricResult.Status {
				case status.EvalStatusPassed:
					// Keep the aggregate status passed unless another metric lowers it.
				case status.EvalStatusUnknown:
					normalized.OverallStatus = status.EvalStatusUnknown
				case status.EvalStatusNotEvaluated:
					if normalized.OverallStatus == status.EvalStatusPassed {
						normalized.OverallStatus = status.EvalStatusNotEvaluated
					}
				case status.EvalStatusFailed:
					if normalized.OverallStatus == status.EvalStatusPassed {
						normalized.OverallStatus = status.EvalStatusFailed
					}
				default:
					normalized.OverallStatus = status.EvalStatusUnknown
				}
			}
			if len(caseResult.Metrics) == 0 {
				return nil, fmt.Errorf("PromptIter case %q has no metrics", evalCase.EvalCaseID)
			}
			caseResult.Score /= float64(len(caseResult.Metrics))
			normalized.Cases = append(normalized.Cases, caseResult)
			normalized.Usage = AddUsage(normalized.Usage, caseResult.Trace.Usage)
		}
	}
	if len(normalized.Cases) == 0 {
		return nil, errors.New("PromptIter evaluation result has no cases")
	}
	return normalized, nil
}

func normalizeTrace(executionTrace *atrace.Trace) Trace {
	if executionTrace == nil {
		return Trace{Status: string(atrace.TraceStatusIncomplete), Steps: []TraceStep{}}
	}
	result := Trace{
		Status: string(executionTrace.Status),
		Steps:  make([]TraceStep, 0, len(executionTrace.Steps)),
		Usage:  traceUsage(executionTrace),
	}
	if executionTrace.Output != nil {
		result.Output = executionTrace.Output.Text
	}
	for _, step := range executionTrace.Steps {
		item := TraceStep{StepID: step.StepID, NodeType: step.NodeType, Error: step.Error}
		if step.Input != nil {
			item.Input = step.Input.Text
		}
		if step.Output != nil {
			item.Output = step.Output.Text
		}
		result.Steps = append(result.Steps, item)
	}
	return result
}

func traceUsage(executionTrace *atrace.Trace) Usage {
	if executionTrace == nil {
		return Usage{}
	}
	usage := Usage{Measured: true}
	if executionTrace.EndedAt.After(executionTrace.StartedAt) {
		usage.Duration = executionTrace.EndedAt.Sub(executionTrace.StartedAt)
	}
	if executionTrace.Usage != nil {
		usage.PromptTokens = executionTrace.Usage.PromptTokens
		usage.CompletionTokens = executionTrace.Usage.CompletionTokens
		usage.TotalTokens = executionTrace.Usage.TotalTokens
	}
	stepTokens := Usage{}
	for _, step := range executionTrace.Steps {
		if step.NodeType == "tool" {
			usage.ToolCalls++
		}
		if step.NodeType == "llm" {
			usage.ModelCalls++
		}
		if step.Usage != nil {
			stepTokens.PromptTokens += step.Usage.PromptTokens
			stepTokens.CompletionTokens += step.Usage.CompletionTokens
			stepTokens.TotalTokens += step.Usage.TotalTokens
		}
	}
	if executionTrace.Usage == nil {
		usage.PromptTokens = stepTokens.PromptTokens
		usage.CompletionTokens = stepTokens.CompletionTokens
		usage.TotalTokens = stepTokens.TotalTokens
	}
	if usage.ModelCalls == 0 && executionTrace.Usage != nil {
		usage.ModelCalls = 1
	}
	return usage
}

// AddUsage combines independently measured usage without changing provenance.
func AddUsage(left, right Usage) Usage {
	return Usage{
		PromptTokens:     left.PromptTokens + right.PromptTokens,
		CompletionTokens: left.CompletionTokens + right.CompletionTokens,
		TotalTokens:      left.TotalTokens + right.TotalTokens,
		ModelCalls:       left.ModelCalls + right.ModelCalls,
		ToolCalls:        left.ToolCalls + right.ToolCalls,
		Duration:         left.Duration + right.Duration,
		Measured:         left.Measured && right.Measured,
	}
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
