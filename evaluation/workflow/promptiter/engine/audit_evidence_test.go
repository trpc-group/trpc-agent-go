//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestSummarizeEvaluationUsageHandlesIncompleteEvidence(t *testing.T) {
	assert.Equal(t, int64(0), summarizeEvaluationUsage(nil).TotalTokens)

	result := &evaluation.EvaluationResult{EvalCases: []*evaluation.EvaluationCaseResult{
		nil,
		{},
		{RunDetails: []*evaluation.EvaluationCaseRunDetails{
			nil,
			{},
			{Inference: &evaluation.EvaluationInferenceDetails{}},
			{Inference: &evaluation.EvaluationInferenceDetails{ExecutionTraces: []*atrace.Trace{
				nil,
				{Usage: &model.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}},
			}}},
		}},
	}}
	usage := summarizeEvaluationUsage(result)
	assert.Equal(t, 0, usage.Calls)
	assert.Equal(t, int64(5), usage.TotalTokens)
	assert.False(t, usage.Complete)
}

func TestSummarizeTraceUsageCountsCallsAndAggregateFallback(t *testing.T) {
	assert.Equal(t, 0, summarizeTraceUsage(nil).Calls)
	assert.Equal(t, 0, summarizeTraceUsage(&atrace.Trace{}).Calls)

	aggregated := summarizeTraceUsage(&atrace.Trace{
		Usage: &model.Usage{PromptTokens: 4, CompletionTokens: 3, TotalTokens: 7},
	})
	assert.Equal(t, 0, aggregated.Calls)
	assert.Equal(t, int64(7), aggregated.TotalTokens)
	assert.False(t, aggregated.Complete)

	complete := summarizeTraceUsage(&atrace.Trace{
		Usage: &model.Usage{PromptTokens: 4, CompletionTokens: 3, TotalTokens: 7},
		Steps: []atrace.Step{{
			Usage: &model.Usage{PromptTokens: 4, CompletionTokens: 3, TotalTokens: 7},
		}},
	})
	assert.Equal(t, 1, complete.Calls)
	assert.Equal(t, int64(7), complete.TotalTokens)
	assert.True(t, complete.Complete)

	partial := summarizeTraceUsage(&atrace.Trace{Steps: []atrace.Step{
		{},
		{Usage: &model.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}},
		{Usage: &model.Usage{PromptTokens: 4, CompletionTokens: 5, TotalTokens: 9}},
	}})
	assert.Equal(t, 2, partial.Calls)
	assert.Equal(t, int64(5), partial.PromptTokens)
	assert.Equal(t, int64(7), partial.CompletionTokens)
	assert.Equal(t, int64(12), partial.TotalTokens)
	assert.False(t, partial.Complete)
}

func TestRepresentativeRunEvidenceRejectsMalformedRunPairs(t *testing.T) {
	tests := []struct {
		name    string
		value   *evaluation.EvaluationCaseResult
		message string
	}{
		{"nil detail", &evaluation.EvaluationCaseResult{RunDetails: []*evaluation.EvaluationCaseRunDetails{nil}}, "run detail at index 0 is nil"},
		{"invalid detail id", &evaluation.EvaluationCaseResult{RunDetails: []*evaluation.EvaluationCaseRunDetails{{}}}, "invalid id 0"},
		{"duplicate detail", &evaluation.EvaluationCaseResult{RunDetails: []*evaluation.EvaluationCaseRunDetails{{RunID: 1}, {RunID: 1}}}, "duplicate run detail id 1"},
		{"nil result", &evaluation.EvaluationCaseResult{RunDetails: []*evaluation.EvaluationCaseRunDetails{{RunID: 1}}, EvalCaseResults: []*evalresult.EvalCaseResult{nil}}, "run result at index 0 is nil"},
		{"invalid result id", &evaluation.EvaluationCaseResult{RunDetails: []*evaluation.EvaluationCaseRunDetails{{RunID: 1}}, EvalCaseResults: []*evalresult.EvalCaseResult{{}}}, "invalid id 0"},
		{"duplicate result", &evaluation.EvaluationCaseResult{RunDetails: []*evaluation.EvaluationCaseRunDetails{{RunID: 1}}, EvalCaseResults: []*evalresult.EvalCaseResult{{RunID: 1}, {RunID: 1}}}, "duplicate run result id 1"},
		{"missing detail", &evaluation.EvaluationCaseResult{RunDetails: []*evaluation.EvaluationCaseRunDetails{{RunID: 1}}, EvalCaseResults: []*evalresult.EvalCaseResult{{RunID: 2}}}, "no matching run detail"},
		{"no results", &evaluation.EvaluationCaseResult{RunDetails: []*evaluation.EvaluationCaseRunDetails{{RunID: 1}}}, "no non-nil run result"},
		{"count mismatch", &evaluation.EvaluationCaseResult{RunDetails: []*evaluation.EvaluationCaseRunDetails{{RunID: 1}, {RunID: 2}}, EvalCaseResults: []*evalresult.EvalCaseResult{{RunID: 1}}}, "does not match"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := representativeRunEvidence(test.value)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.message)
		})
	}
}

func TestRepresentativeRunEvidencePrefersFailureAndFallsBack(t *testing.T) {
	details := []*evaluation.EvaluationCaseRunDetails{{RunID: 1}, {RunID: 2}}
	runs := []*evalresult.EvalCaseResult{
		{RunID: 1},
		{RunID: 2, OverallEvalMetricResults: []*evalresult.EvalMetricResult{{
			MetricName: "quality", EvalStatus: status.EvalStatusFailed,
		}}},
	}
	failed, detail, err := representativeRunEvidence(&evaluation.EvaluationCaseResult{
		RunDetails:      details,
		EvalCaseResults: runs,
		MetricResults: []*evalresult.EvalMetricResult{{
			MetricName: "quality", EvalStatus: status.EvalStatusFailed,
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, failed.RunID)
	assert.Equal(t, 2, detail.RunID)

	fallback, detail, err := representativeRunEvidence(&evaluation.EvaluationCaseResult{
		RunDetails: details,
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{RunID: 1},
			{RunID: 2},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, fallback.RunID)
	assert.Equal(t, 1, detail.RunID)
}

func TestRunFailsAnyMetricContracts(t *testing.T) {
	failed := &evalresult.EvalMetricResult{MetricName: "quality", EvalStatus: status.EvalStatusFailed}
	assert.False(t, runFailsAnyMetric(nil, nil))
	assert.True(t, runFailsAnyMetric(&evalresult.EvalCaseResult{ErrorMessage: "failed"}, map[string]struct{}{"other": {}}))
	assert.True(t, runFailsAnyMetric(&evalresult.EvalCaseResult{OverallEvalMetricResults: []*evalresult.EvalMetricResult{nil, failed}}, nil))
	assert.True(t, runFailsAnyMetric(&evalresult.EvalCaseResult{OverallEvalMetricResults: []*evalresult.EvalMetricResult{failed}}, map[string]struct{}{"quality": {}}))
	assert.False(t, runFailsAnyMetric(&evalresult.EvalCaseResult{OverallEvalMetricResults: []*evalresult.EvalMetricResult{failed}}, map[string]struct{}{"safety": {}}))
}

func TestAggregateMetricEvidenceOwnsDetailsAndProvidesFailureReason(t *testing.T) {
	rubric := &evalresult.RubricScore{ID: "r1", Score: .5}
	details := &evalresult.EvalMetricResultDetails{Reason: "aggregate reason", RubricScores: []*evalresult.RubricScore{nil, rubric}}
	aggregated := aggregateMetricEvidence([]*evalresult.EvalMetricResult{
		nil,
		{MetricName: "existing", EvalStatus: status.EvalStatusFailed, Details: details},
		{MetricName: "from-run", EvalStatus: status.EvalStatusFailed},
		{MetricName: "synthetic", EvalStatus: status.EvalStatusFailed, Score: .4, Threshold: .7},
	}, []*evalresult.EvalCaseResult{nil, {OverallEvalMetricResults: []*evalresult.EvalMetricResult{
		nil,
		{MetricName: "from-run", EvalStatus: status.EvalStatusPassed},
		{MetricName: "from-run", EvalStatus: status.EvalStatusFailed, Details: &evalresult.EvalMetricResultDetails{Reason: "run reason"}},
	}}})

	require.Len(t, aggregated, 3)
	assert.Equal(t, "aggregate reason", aggregated[0].Details.Reason)
	assert.Equal(t, "run reason", aggregated[1].Details.Reason)
	assert.Contains(t, aggregated[2].Details.Reason, "aggregate score 0.400000")
	require.Len(t, aggregated[0].Details.RubricScores, 2)
	assert.Nil(t, aggregated[0].Details.RubricScores[0])
	require.NotSame(t, rubric, aggregated[0].Details.RubricScores[1])
	aggregated[0].Details.RubricScores[1].Score = 1
	assert.Equal(t, .5, rubric.Score)
	assert.Nil(t, cloneMetricDetails(nil))
}

func TestFailedMetricTracesRejectsInconsistentEvidence(t *testing.T) {
	_, err := failedMetricTraces(nil, nil)
	assert.EqualError(t, err, "evaluation case result is nil")

	tests := []struct {
		name    string
		value   *evaluation.EvaluationCaseResult
		message string
	}{
		{"invalid detail", &evaluation.EvaluationCaseResult{RunDetails: []*evaluation.EvaluationCaseRunDetails{nil}}, "run detail at index 0 is invalid"},
		{"duplicate detail", &evaluation.EvaluationCaseResult{RunDetails: []*evaluation.EvaluationCaseRunDetails{{RunID: 1}, {RunID: 1}}}, "duplicate run detail id 1"},
		{"missing detail", &evaluation.EvaluationCaseResult{
			MetricResults:   []*evalresult.EvalMetricResult{{MetricName: "quality", EvalStatus: status.EvalStatusFailed}},
			EvalCaseResults: []*evalresult.EvalCaseResult{{RunID: 2, ErrorMessage: "failed"}},
		}, "no matching run detail"},
		{"missing failing run", &evaluation.EvaluationCaseResult{
			RunDetails:      []*evaluation.EvaluationCaseRunDetails{{RunID: 1}},
			MetricResults:   []*evalresult.EvalMetricResult{nil, {MetricName: "quality", EvalStatus: status.EvalStatusFailed}},
			EvalCaseResults: []*evalresult.EvalCaseResult{nil, {RunID: 1}},
		}, "has no failing run evidence"},
		{"trace extraction", &evaluation.EvaluationCaseResult{
			EvalCaseID:      "case",
			RunDetails:      []*evaluation.EvaluationCaseRunDetails{{RunID: 1}},
			MetricResults:   []*evalresult.EvalMetricResult{{MetricName: "quality", EvalStatus: status.EvalStatusFailed}},
			EvalCaseResults: []*evalresult.EvalCaseResult{{RunID: 1, ErrorMessage: "failed"}},
		}, "extract trace for metric"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := failedMetricTraces(nil, test.value)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.message)
		})
	}
}
