//
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
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// NormalizeAgentEvaluation converts a public Evaluation Service result.
func NormalizeAgentEvaluation(result *evaluation.EvaluationResult) (*EvaluationResult, error) {
	if result == nil {
		return nil, errors.New("agent evaluation result is nil")
	}
	converted := &promptiterengine.EvaluationResult{EvalSets: []promptiterengine.EvalSetResult{{
		EvalSetID: result.EvalSetID, Cases: make([]promptiterengine.CaseResult, 0, len(result.EvalCases)),
	}}}
	total := 0.0
	metrics := 0
	for _, evalCase := range result.EvalCases {
		item, score, count, err := convertAgentCase(result.EvalSetID, evalCase)
		if err != nil {
			return nil, err
		}
		converted.EvalSets[0].Cases = append(converted.EvalSets[0].Cases, *item)
		total += score
		metrics += count
	}
	if metrics == 0 {
		return nil, errors.New("agent evaluation has no metric scores")
	}
	converted.OverallScore = total / float64(metrics)
	converted.EvalSets[0].OverallScore = converted.OverallScore
	return NormalizeEvaluation(converted)
}

func convertAgentCase(
	evalSetID string,
	input *evaluation.EvaluationCaseResult,
) (*promptiterengine.CaseResult, float64, int, error) {
	if input == nil || len(input.EvalCaseResults) == 0 || len(input.RunDetails) == 0 {
		return nil, 0, 0, errors.New("agent evaluation case is incomplete")
	}
	run := input.EvalCaseResults[0]
	detail := input.RunDetails[0]
	if run == nil || detail == nil || detail.Inference == nil || len(detail.Inference.ExecutionTraces) != 1 {
		return nil, 0, 0, errors.New("agent evaluation run details are incomplete")
	}
	metrics := make([]promptiterengine.MetricResult, 0, len(run.OverallEvalMetricResults))
	total := 0.0
	evaluated := 0
	for _, item := range run.OverallEvalMetricResults {
		if item == nil {
			continue
		}
		reason := ""
		if item.Details != nil {
			reason = item.Details.Reason
		}
		metrics = append(metrics, promptiterengine.MetricResult{
			MetricName: item.MetricName, Score: item.Score, Status: item.EvalStatus, Reason: reason,
		})
		if item.EvalStatus != status.EvalStatusNotEvaluated {
			total += item.Score
			evaluated++
		}
	}
	return &promptiterengine.CaseResult{
		EvalSetID: evalSetID, EvalCaseID: input.EvalCaseID,
		Trace: detail.Inference.ExecutionTraces[0], Metrics: metrics,
	}, total, evaluated, nil
}

// NormalizeEvaluation converts one PromptIter evaluation result for reporting.
func NormalizeEvaluation(result *promptiterengine.EvaluationResult) (*EvaluationResult, error) {
	if result == nil {
		return nil, errors.New("evaluation result is nil")
	}
	normalized := &EvaluationResult{
		OverallScore: result.OverallScore,
		Cases:        make([]CaseResult, 0),
	}
	for _, evalSet := range result.EvalSets {
		for _, evalCase := range evalSet.Cases {
			caseResult, err := normalizeCase(evalCase)
			if err != nil {
				return nil, fmt.Errorf("normalize case %q: %w", evalCase.EvalCaseID, err)
			}
			normalized.Cases = append(normalized.Cases, *caseResult)
			normalized.Usage = AddUsage(normalized.Usage, caseResult.Trace.Usage)
		}
	}
	sort.SliceStable(normalized.Cases, func(i, j int) bool {
		if normalized.Cases[i].EvalSetID != normalized.Cases[j].EvalSetID {
			return normalized.Cases[i].EvalSetID < normalized.Cases[j].EvalSetID
		}
		return normalized.Cases[i].CaseID < normalized.Cases[j].CaseID
	})
	if len(normalized.Cases) == 0 {
		return nil, errors.New("evaluation result has no cases")
	}
	overallScore, err := evaluationScoreFromCases(normalized.Cases)
	if err != nil {
		return nil, fmt.Errorf("compute normalized overall score: %w", err)
	}
	normalized.OverallScore = overallScore
	return normalized, nil
}

func normalizeCase(input promptiterengine.CaseResult) (*CaseResult, error) {
	if input.EvalSetID == "" || input.EvalCaseID == "" {
		return nil, errors.New("case identity is empty")
	}
	metrics := make([]MetricResult, 0, len(input.Metrics))
	aggregate := scoreAccumulator{}
	passedCase := true
	for _, item := range input.Metrics {
		if item.MetricName == "" {
			return nil, errors.New("metric name is empty")
		}
		metrics = append(metrics, MetricResult{
			Name: item.MetricName, Score: item.Score, Status: item.Status, Reason: item.Reason,
		})
		if item.Status != status.EvalStatusNotEvaluated {
			aggregate.total += item.Score
			aggregate.metrics++
		}
		if item.Status != status.EvalStatusPassed {
			passedCase = false
		}
	}
	if len(metrics) == 0 {
		return nil, errors.New("case has no metrics")
	}
	sort.SliceStable(metrics, func(i, j int) bool { return metrics[i].Name < metrics[j].Name })
	traceSummary, err := summarizeTrace(input.Trace)
	if err != nil {
		return nil, err
	}
	if traceIsFailure(traceSummary.Status) {
		passedCase = false
	}
	return &CaseResult{
		EvalSetID: input.EvalSetID,
		CaseID:    input.EvalCaseID,
		Score:     averageScore(aggregate),
		Passed:    passedCase,
		Metrics:   metrics,
		Trace:     *traceSummary,
	}, nil
}

func traceIsFailure(value string) bool {
	return value == string(atrace.TraceStatusFailed) || value == string(atrace.TraceStatusIncomplete)
}

func summarizeTrace(input *atrace.Trace) (*TraceSummary, error) {
	if input == nil {
		return nil, errors.New("trace is nil")
	}
	if err := validateTraceStatus(input.Status); err != nil {
		return nil, err
	}
	result := &TraceSummary{
		Status: string(input.Status),
		Steps:  make([]TraceStep, 0, len(input.Steps)),
	}
	if input.Output != nil {
		result.Output = input.Output.Text
	}
	if input.Usage != nil {
		result.Usage.PromptTokens = input.Usage.PromptTokens
		result.Usage.CompletionTokens = input.Usage.CompletionTokens
		result.Usage.TotalTokens = input.Usage.TotalTokens
	}
	if !input.EndedAt.Before(input.StartedAt) {
		result.Usage.Duration = input.EndedAt.Sub(input.StartedAt)
	}
	for _, item := range input.Steps {
		switch item.NodeType {
		case string(structure.NodeKindLLM):
			result.Usage.ModelCalls++
		case string(structure.NodeKindTool):
			result.Usage.ToolCalls++
		}
		if item.NodeType == string(structure.NodeKindLLM) && item.Error == "" {
			continue
		}
		step := TraceStep{
			StepID: item.StepID, NodeID: item.NodeID, NodeType: item.NodeType, Error: item.Error,
		}
		preserveSnapshots := item.Error != "" || item.NodeType == string(structure.NodeKindTool)
		if preserveSnapshots {
			if item.Input != nil {
				step.Input = item.Input.Text
			}
			if item.Output != nil {
				step.Output = item.Output.Text
			}
		}
		result.Steps = append(result.Steps, step)
	}
	return result, nil
}

func validateTraceStatus(value atrace.TraceStatus) error {
	switch value {
	case atrace.TraceStatusCompleted, atrace.TraceStatusIncomplete, atrace.TraceStatusFailed:
		return nil
	default:
		return fmt.Errorf("trace status %q is invalid", value)
	}
}

// AddUsage merges two usage summaries.
func AddUsage(left, right UsageSummary) UsageSummary {
	return UsageSummary{
		MonetaryCostAvailable: left.MonetaryCostAvailable && right.MonetaryCostAvailable,
		MonetaryCost:          left.MonetaryCost + right.MonetaryCost,
		PromptTokens:          left.PromptTokens + right.PromptTokens,
		CompletionTokens:      left.CompletionTokens + right.CompletionTokens,
		TotalTokens:           left.TotalTokens + right.TotalTokens,
		ModelCalls:            left.ModelCalls + right.ModelCalls,
		ToolCalls:             left.ToolCalls + right.ToolCalls,
		Duration:              left.Duration + right.Duration,
	}
}

// Milliseconds returns duration milliseconds for templates.
func Milliseconds(value time.Duration) int64 {
	return value.Milliseconds()
}
