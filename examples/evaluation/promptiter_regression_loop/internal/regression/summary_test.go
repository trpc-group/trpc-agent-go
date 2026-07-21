//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNormalizeEvaluationSummarizesAndSortsCases(t *testing.T) {
	start := time.Unix(10, 0)
	input := &promptiterengine.EvaluationResult{
		OverallScore: 0.75,
		EvalSets: []promptiterengine.EvalSetResult{{
			EvalSetID: testEvalSetID,
			Cases: []promptiterengine.CaseResult{
				engineCase("b", status.EvalStatusFailed, start),
				engineCase("a", status.EvalStatusPassed, start),
			},
		}},
	}

	result, err := NormalizeEvaluation(input)
	require.NoError(t, err)
	require.Len(t, result.Cases, 2)
	assert.Equal(t, []string{"a", "b"}, []string{result.Cases[0].CaseID, result.Cases[1].CaseID})
	assert.True(t, result.Cases[0].Passed)
	assert.False(t, result.Cases[1].Passed)
	assert.Equal(t, 6, result.Usage.TotalTokens)
	assert.Equal(t, 2, result.Usage.ModelCalls)
	assert.Equal(t, 2, result.Usage.ToolCalls)
	assert.Equal(t, 4*time.Second, result.Usage.Duration)
	assert.Equal(t, "final-a", result.Cases[0].Trace.Output)
	require.Len(t, result.Cases[0].Trace.Steps, 1)
	assert.Equal(t, "tool-input-a", result.Cases[0].Trace.Steps[0].Input)
}

func TestNormalizeAgentEvaluationConvertsPublicResult(t *testing.T) {
	input := agentEvaluation("case", status.EvalStatusFailed)
	input.EvalCases[0].EvalCaseResults[0].OverallEvalMetricResults[1].Details =
		&evalresult.EvalMetricResultDetails{Reason: "mismatch"}

	result, err := NormalizeAgentEvaluation(input)
	require.NoError(t, err)
	require.Len(t, result.Cases, 1)
	assert.Equal(t, "case", result.Cases[0].CaseID)
	assert.Equal(t, "mismatch", result.Cases[0].Metrics[0].Reason)
	assert.Equal(t, 3, result.Usage.TotalTokens)
}

func TestNormalizeAgentEvaluationRejectsIncompleteResults(t *testing.T) {
	complete := agentEvaluation("case", status.EvalStatusPassed)
	tests := []struct {
		name  string
		input *evaluation.EvaluationResult
	}{
		{name: "nil"},
		{name: "no cases", input: &evaluation.EvaluationResult{EvalSetID: testEvalSetID}},
		{name: "nil case", input: &evaluation.EvaluationResult{
			EvalSetID: testEvalSetID, EvalCases: []*evaluation.EvaluationCaseResult{nil},
		}},
		{name: "missing run details", input: &evaluation.EvaluationResult{
			EvalSetID: testEvalSetID,
			EvalCases: []*evaluation.EvaluationCaseResult{{
				EvalCaseID: "case", EvalCaseResults: complete.EvalCases[0].EvalCaseResults,
			}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeAgentEvaluation(test.input)
			require.Error(t, err)
		})
	}
}

func TestNormalizeEvaluationRejectsIncompleteInput(t *testing.T) {
	validCase := engineCase("a", status.EvalStatusPassed, time.Unix(10, 0))
	tests := []struct {
		name     string
		input    *promptiterengine.EvaluationResult
		contains string
	}{
		{name: "nil result", contains: "evaluation result is nil"},
		{name: "no cases", input: &promptiterengine.EvaluationResult{}, contains: "has no cases"},
		{name: "empty identity", input: engineEvaluation(promptiterengine.CaseResult{}), contains: "case identity is empty"},
		{name: "no metrics", input: engineEvaluation(promptiterengine.CaseResult{EvalSetID: testEvalSetID, EvalCaseID: "a"}), contains: "case has no metrics"},
		{name: "nil trace", input: engineEvaluation(promptiterengine.CaseResult{EvalSetID: testEvalSetID, EvalCaseID: "a", Metrics: validCase.Metrics}), contains: "trace is nil"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeEvaluation(test.input)
			require.ErrorContains(t, err, test.contains)
		})
	}
}

func TestAddUsageAndMilliseconds(t *testing.T) {
	left := UsageSummary{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3, ModelCalls: 1, Duration: time.Second}
	right := UsageSummary{PromptTokens: 4, CompletionTokens: 5, TotalTokens: 9, ToolCalls: 2, Duration: 500 * time.Millisecond}
	result := AddUsage(left, right)
	assert.Equal(t, 5, result.PromptTokens)
	assert.Equal(t, 7, result.CompletionTokens)
	assert.Equal(t, 12, result.TotalTokens)
	assert.Equal(t, 1, result.ModelCalls)
	assert.Equal(t, 2, result.ToolCalls)
	assert.Equal(t, int64(1500), Milliseconds(result.Duration))
}

func TestNormalizeEvaluationHandlesNilSnapshotsAndNegativeTraceRange(t *testing.T) {
	start := time.Unix(10, 0)
	item := engineCase("a", status.EvalStatusPassed, start)
	item.Trace.Output = nil
	item.Trace.Usage = nil
	item.Trace.EndedAt = start.Add(-time.Second)
	item.Trace.Steps[0].Input = nil
	item.Trace.Steps[0].Output = &atrace.Snapshot{Text: "step-output"}
	item.Trace.Steps[0].Error = "step failed"

	result, err := NormalizeEvaluation(engineEvaluation(item))
	require.NoError(t, err)
	assert.Empty(t, result.Cases[0].Trace.Output)
	assert.Zero(t, result.Usage.TotalTokens)
	assert.Zero(t, result.Usage.Duration)
	assert.Empty(t, result.Cases[0].Trace.Steps[0].Input)
	assert.Equal(t, "step-output", result.Cases[0].Trace.Steps[0].Output)
}

func TestTraceFailureMakesCaseFailAndProducesAttribution(t *testing.T) {
	input := engineEvaluation(engineCase("case", status.EvalStatusPassed, time.Now()))
	input.EvalSets[0].Cases[0].Trace.Status = atrace.TraceStatusFailed
	result, err := NormalizeEvaluation(input)
	require.NoError(t, err)
	require.Len(t, result.Cases, 1)
	assert.False(t, result.Cases[0].Passed)

	attributed := Attribute(result, AttributionCatalog{})
	require.Len(t, attributed.Items, 1)
	assert.Equal(t, traceFailureMetric, attributed.Items[0].Metric)
	assert.Equal(t, CategoryExecutionError, attributed.Items[0].Category)
}

func TestNormalizeEvaluationRejectsUnknownTraceStatus(t *testing.T) {
	input := engineEvaluation(engineCase("case", status.EvalStatusPassed, time.Now()))
	input.EvalSets[0].Cases[0].Trace.Status = "unknown"
	_, err := NormalizeEvaluation(input)
	require.ErrorContains(t, err, "trace status")
}

func engineCase(caseID string, evalStatus status.EvalStatus, start time.Time) promptiterengine.CaseResult {
	score := 0.0
	if evalStatus == status.EvalStatusPassed {
		score = 1
	}
	return promptiterengine.CaseResult{
		EvalSetID:  testEvalSetID,
		EvalCaseID: caseID,
		Metrics: []promptiterengine.MetricResult{{
			MetricName: "quality", Score: score, Status: evalStatus, Reason: "reason",
		}},
		Trace: &atrace.Trace{
			Status: atrace.TraceStatusCompleted, StartedAt: start, EndedAt: start.Add(2 * time.Second),
			Output: &atrace.Snapshot{Text: "final-" + caseID},
			Usage:  &model.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
			Steps: []atrace.Step{
				{StepID: "llm-" + caseID, NodeID: "agent", NodeType: string(structure.NodeKindLLM), Input: &atrace.Snapshot{Text: "input-" + caseID}},
				{
					StepID: "tool-" + caseID, NodeID: "tool", NodeType: string(structure.NodeKindTool),
					Input:  &atrace.Snapshot{Text: "tool-input-" + caseID},
					Output: &atrace.Snapshot{Text: "tool-output-" + caseID},
				},
			},
		},
	}
}

func engineEvaluation(item promptiterengine.CaseResult) *promptiterengine.EvaluationResult {
	return &promptiterengine.EvaluationResult{EvalSets: []promptiterengine.EvalSetResult{{
		EvalSetID: testEvalSetID, Cases: []promptiterengine.CaseResult{item},
	}}}
}

func agentEvaluation(caseID string, evalStatus status.EvalStatus) *evaluation.EvaluationResult {
	engineResult := engineCase(caseID, evalStatus, time.Unix(10, 0))
	metricResult := &evalresult.EvalMetricResult{
		MetricName: "quality", Score: engineResult.Metrics[0].Score, EvalStatus: evalStatus,
	}
	return &evaluation.EvaluationResult{
		EvalSetID: testEvalSetID,
		EvalCases: []*evaluation.EvaluationCaseResult{{
			EvalCaseID: caseID,
			EvalCaseResults: []*evalresult.EvalCaseResult{{
				OverallEvalMetricResults: []*evalresult.EvalMetricResult{nil, metricResult},
			}},
			RunDetails: []*evaluation.EvaluationCaseRunDetails{{
				Inference: &evaluation.EvaluationInferenceDetails{
					ExecutionTraces: []*atrace.Trace{engineResult.Trace},
				},
			}},
		}},
	}
}
