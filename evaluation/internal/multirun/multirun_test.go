//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package multirun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestSummarizeMultiRunNilEvalSetResult(t *testing.T) {
	err := SummarizeMultiRun(nil, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "eval set result is nil")
}

func TestSummarizeMultiRunNegativeExpectedNumRuns(t *testing.T) {
	result := &evalresult.EvalSetResult{
		EvalSetID: "set",
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{EvalSetID: "set", EvalID: "A", RunID: 1, FinalEvalStatus: status.EvalStatusPassed},
		},
	}
	err := SummarizeMultiRun(result, -1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected num runs is negative")
}

func TestSummarizeMultiRunEmptyResultsUsesExpectedNumRuns(t *testing.T) {
	result := &evalresult.EvalSetResult{
		EvalSetID:       "set",
		EvalCaseResults: []*evalresult.EvalCaseResult{},
	}

	err := SummarizeMultiRun(result, 2)
	assert.NoError(t, err)

	assert.NotNil(t, result.Summary)
	if result.Summary == nil {
		return
	}

	assert.Equal(t, 2, result.Summary.NumRuns)
	assert.Equal(t, status.EvalStatusNotEvaluated, result.Summary.OverallStatus)
	assert.Len(t, result.Summary.RunSummaries, 2)
	assert.NotNil(t, result.Summary.RunStatusCounts)
	if result.Summary.RunStatusCounts != nil {
		assert.Equal(t, 2, result.Summary.RunStatusCounts.NotEvaluated)
	}
	assert.Len(t, result.Summary.EvalCaseSummaries, 0)

	if len(result.Summary.RunSummaries) > 0 {
		assert.Equal(t, 1, result.Summary.RunSummaries[0].RunID)
		assert.Equal(t, status.EvalStatusNotEvaluated, result.Summary.RunSummaries[0].OverallStatus)
		assert.Nil(t, result.Summary.RunSummaries[0].CaseStatusCounts)
		assert.Nil(t, result.Summary.RunSummaries[0].MetricSummaries)
	}
	if len(result.Summary.RunSummaries) > 1 {
		assert.Equal(t, 2, result.Summary.RunSummaries[1].RunID)
		assert.Equal(t, status.EvalStatusNotEvaluated, result.Summary.RunSummaries[1].OverallStatus)
		assert.Nil(t, result.Summary.RunSummaries[1].CaseStatusCounts)
		assert.Nil(t, result.Summary.RunSummaries[1].MetricSummaries)
	}
}

func TestSummarizeMultiRunEmptyEvalIDReturnsError(t *testing.T) {
	result := &evalresult.EvalSetResult{
		EvalSetID: "set",
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{EvalSetID: "set", EvalID: "", RunID: 1, FinalEvalStatus: status.EvalStatusPassed},
		},
	}
	err := SummarizeMultiRun(result, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "eval id at index 0 is empty")
}

func TestSummarizeMultiRunMissingRunIDReturnsError(t *testing.T) {
	result := &evalresult.EvalSetResult{
		EvalSetID: "set",
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{EvalSetID: "set", EvalID: "A", RunID: 0, FinalEvalStatus: status.EvalStatusPassed},
		},
	}
	err := SummarizeMultiRun(result, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "run id at index 0 is not set")
}

func TestSummarizeMultiRunRunIDExceedsExpectedNumRuns(t *testing.T) {
	result := &evalresult.EvalSetResult{
		EvalSetID: "set",
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{EvalSetID: "set", EvalID: "A", RunID: 2, FinalEvalStatus: status.EvalStatusPassed},
		},
	}
	err := SummarizeMultiRun(result, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds expected num runs")
}

func TestSummarizeMultiRunUnexpectedEvalStatusReturnsError(t *testing.T) {
	result := &evalresult.EvalSetResult{
		EvalSetID: "set",
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{EvalSetID: "set", EvalID: "A", RunID: 1, FinalEvalStatus: status.EvalStatusUnknown},
		},
	}
	err := SummarizeMultiRun(result, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected eval status")
}

func TestSummarizeMultiRunMetricSummariesIncludeStatusCountsButExcludeNotEvaluatedFromAverage(t *testing.T) {
	result := &evalresult.EvalSetResult{
		EvalSetID: "set",
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{
				EvalSetID:       "set",
				EvalID:          "A",
				RunID:           1,
				FinalEvalStatus: status.EvalStatusPassed,
				OverallEvalMetricResults: []*evalresult.EvalMetricResult{
					{MetricName: "m", Score: 0, EvalStatus: status.EvalStatusNotEvaluated, Threshold: 1},
				},
			},
			{
				EvalSetID:       "set",
				EvalID:          "B",
				RunID:           1,
				FinalEvalStatus: status.EvalStatusPassed,
				OverallEvalMetricResults: []*evalresult.EvalMetricResult{
					{MetricName: "m", Score: 2, EvalStatus: status.EvalStatusPassed, Threshold: 1},
				},
			},
		},
	}

	err := SummarizeMultiRun(result, 1)
	assert.NoError(t, err)

	assert.NotNil(t, result.Summary)
	if result.Summary == nil {
		return
	}

	assert.Equal(t, 1, result.Summary.NumRuns)
	assert.Len(t, result.Summary.RunSummaries, 1)
	if len(result.Summary.RunSummaries) == 0 {
		return
	}

	runSummary := result.Summary.RunSummaries[0]
	assert.NotNil(t, runSummary)
	if runSummary == nil {
		return
	}

	assert.Equal(t, 1, runSummary.RunID)
	assert.Equal(t, status.EvalStatusPassed, runSummary.OverallStatus)
	assert.NotNil(t, runSummary.CaseStatusCounts)
	if runSummary.CaseStatusCounts != nil {
		assert.Equal(t, 2, runSummary.CaseStatusCounts.Passed)
	}
	assert.Len(t, runSummary.MetricSummaries, 1)
	if len(runSummary.MetricSummaries) == 0 {
		return
	}

	metricSummary := runSummary.MetricSummaries[0]
	assert.NotNil(t, metricSummary)
	if metricSummary == nil {
		return
	}

	assert.Equal(t, "m", metricSummary.MetricName)
	assert.Equal(t, 2.0, metricSummary.AverageScore)
	assert.Equal(t, status.EvalStatusPassed, metricSummary.EvalStatus)
	assert.Equal(t, 1.0, metricSummary.Threshold)
	assert.NotNil(t, metricSummary.StatusCounts)
	if metricSummary.StatusCounts != nil {
		assert.Equal(t, 1, metricSummary.StatusCounts.Passed)
		assert.Equal(t, 1, metricSummary.StatusCounts.NotEvaluated)
	}
}

func TestSummarizeMultiRunCaseRunErrorTurnsNotEvaluatedIntoFailed(t *testing.T) {
	result := &evalresult.EvalSetResult{
		EvalSetID: "set",
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{
				EvalSetID:       "set",
				EvalID:          "A",
				RunID:           1,
				FinalEvalStatus: status.EvalStatusFailed,
				ErrorMessage:    "boom",
			},
		},
	}

	err := SummarizeMultiRun(result, 1)
	assert.NoError(t, err)

	assert.NotNil(t, result.Summary)
	if result.Summary == nil {
		return
	}

	assert.Len(t, result.Summary.EvalCaseSummaries, 1)
	if len(result.Summary.EvalCaseSummaries) == 0 {
		return
	}

	caseSummary := result.Summary.EvalCaseSummaries[0]
	assert.NotNil(t, caseSummary)
	if caseSummary == nil {
		return
	}

	assert.Equal(t, "A", caseSummary.EvalID)
	assert.Equal(t, status.EvalStatusFailed, caseSummary.OverallStatus)
	assert.Nil(t, caseSummary.MetricSummaries)
}

func TestSummarizeMultiRunMetricRunSummariesAreSortedByName(t *testing.T) {
	result := &evalresult.EvalSetResult{
		EvalSetID: "set",
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{
				EvalSetID:       "set",
				EvalID:          "A",
				RunID:           1,
				FinalEvalStatus: status.EvalStatusPassed,
				OverallEvalMetricResults: []*evalresult.EvalMetricResult{
					{MetricName: "b", Score: 1, EvalStatus: status.EvalStatusPassed, Threshold: 1},
					{MetricName: "a", Score: 1, EvalStatus: status.EvalStatusPassed, Threshold: 1},
				},
			},
		},
	}

	err := SummarizeMultiRun(result, 1)
	assert.NoError(t, err)

	assert.NotNil(t, result.Summary)
	if result.Summary == nil {
		return
	}

	assert.Len(t, result.Summary.EvalCaseSummaries, 1)
	if len(result.Summary.EvalCaseSummaries) == 0 {
		return
	}

	caseSummary := result.Summary.EvalCaseSummaries[0]
	assert.NotNil(t, caseSummary)
	if caseSummary == nil {
		return
	}

	assert.Len(t, caseSummary.RunSummaries, 1)
	if len(caseSummary.RunSummaries) == 0 {
		return
	}

	runSummary := caseSummary.RunSummaries[0]
	assert.NotNil(t, runSummary)
	if runSummary == nil {
		return
	}

	assert.Len(t, runSummary.MetricResults, 2)
	if len(runSummary.MetricResults) != 2 {
		return
	}

	assert.Equal(t, "a", runSummary.MetricResults[0].MetricName)
	assert.Equal(t, "b", runSummary.MetricResults[1].MetricName)
}

func TestGroupCaseResultsByRunIDSkipsNilCaseResults(t *testing.T) {
	caseResults := []*evalresult.EvalCaseResult{
		nil,
		{EvalSetID: "set", EvalID: "A", RunID: 1, FinalEvalStatus: status.EvalStatusPassed},
	}

	runCaseResults, numRuns, err := groupCaseResultsByRunID(caseResults, 1)
	assert.NoError(t, err)
	assert.Equal(t, 1, numRuns)
	assert.Len(t, runCaseResults[1], 1)
}

func TestGroupCaseResultsByRunIDEmptyResultsDefaultsToOneRunWhenExpectedZero(t *testing.T) {
	runCaseResults, numRuns, err := groupCaseResultsByRunID(nil, 0)
	assert.NoError(t, err)
	assert.Equal(t, 1, numRuns)
	assert.Empty(t, runCaseResults)
}

func TestSummarizeMultiRunUsesExpectedNumRunsWhenHigherThanMaxRunID(t *testing.T) {
	result := &evalresult.EvalSetResult{
		EvalSetID: "set",
		EvalCaseResults: []*evalresult.EvalCaseResult{
			{
				EvalSetID:       "set",
				EvalID:          "A",
				RunID:           1,
				FinalEvalStatus: status.EvalStatusPassed,
			},
		},
	}

	err := SummarizeMultiRun(result, 3)
	assert.NoError(t, err)
	assert.NotNil(t, result.Summary)
	if result.Summary == nil {
		return
	}

	assert.Equal(t, 3, result.Summary.NumRuns)
	assert.Len(t, result.Summary.RunSummaries, 3)
	assert.NotNil(t, result.Summary.RunStatusCounts)
	if result.Summary.RunStatusCounts != nil {
		assert.Equal(t, 1, result.Summary.RunStatusCounts.Passed)
		assert.Equal(t, 2, result.Summary.RunStatusCounts.NotEvaluated)
	}
}

func TestBuildEvalSetRunSummariesEmptyRunIDs(t *testing.T) {
	summaries, counts, err := buildEvalSetRunSummaries(map[int][]*evalresult.EvalCaseResult{}, nil)
	assert.NoError(t, err)
	assert.Nil(t, summaries)
	assert.Equal(t, evalresult.EvalStatusCounts{}, counts)
}

func TestBuildEvalSetRunSummariesSkipsNilCaseResults(t *testing.T) {
	runCaseResults := map[int][]*evalresult.EvalCaseResult{
		1: {
			nil,
			{EvalSetID: "set", EvalID: "A", RunID: 1, FinalEvalStatus: status.EvalStatusPassed},
		},
	}

	summaries, counts, err := buildEvalSetRunSummaries(runCaseResults, []int{1})
	assert.NoError(t, err)
	assert.Len(t, summaries, 1)
	if len(summaries) == 1 && summaries[0] != nil {
		assert.Equal(t, status.EvalStatusPassed, summaries[0].OverallStatus)
		assert.NotNil(t, summaries[0].CaseStatusCounts)
		if summaries[0].CaseStatusCounts != nil {
			assert.Equal(t, 1, summaries[0].CaseStatusCounts.Passed)
		}
	}
	assert.Equal(t, 1, counts.Passed)
}

func TestBuildEvalSetRunSummariesCountsFailedStatuses(t *testing.T) {
	runCaseResults := map[int][]*evalresult.EvalCaseResult{
		1: {
			{EvalSetID: "set", EvalID: "A", RunID: 1, FinalEvalStatus: status.EvalStatusFailed},
		},
	}

	summaries, counts, err := buildEvalSetRunSummaries(runCaseResults, []int{1})
	assert.NoError(t, err)
	assert.Len(t, summaries, 1)
	if len(summaries) == 1 && summaries[0] != nil {
		assert.Equal(t, status.EvalStatusFailed, summaries[0].OverallStatus)
		assert.NotNil(t, summaries[0].CaseStatusCounts)
		if summaries[0].CaseStatusCounts != nil {
			assert.Equal(t, 1, summaries[0].CaseStatusCounts.Failed)
		}
	}
	assert.Equal(t, 1, counts.Failed)
}

func TestBuildEvalSetRunSummariesCountsNotEvaluatedStatuses(t *testing.T) {
	runCaseResults := map[int][]*evalresult.EvalCaseResult{
		1: {
			{EvalSetID: "set", EvalID: "A", RunID: 1, FinalEvalStatus: status.EvalStatusNotEvaluated},
		},
	}

	summaries, counts, err := buildEvalSetRunSummaries(runCaseResults, []int{1})
	assert.NoError(t, err)
	assert.Len(t, summaries, 1)
	if len(summaries) == 1 && summaries[0] != nil {
		assert.Equal(t, status.EvalStatusNotEvaluated, summaries[0].OverallStatus)
		assert.NotNil(t, summaries[0].CaseStatusCounts)
		if summaries[0].CaseStatusCounts != nil {
			assert.Equal(t, 1, summaries[0].CaseStatusCounts.NotEvaluated)
		}
	}
	assert.Equal(t, 1, counts.NotEvaluated)
}

func TestBuildEvalSetRunSummariesUnexpectedMetricStatusReturnsError(t *testing.T) {
	runCaseResults := map[int][]*evalresult.EvalCaseResult{
		1: {
			{
				EvalSetID:       "set",
				EvalID:          "A",
				RunID:           1,
				FinalEvalStatus: status.EvalStatusPassed,
				OverallEvalMetricResults: []*evalresult.EvalMetricResult{
					{MetricName: "m", EvalStatus: status.EvalStatusUnknown},
				},
			},
		},
	}

	_, _, err := buildEvalSetRunSummaries(runCaseResults, []int{1})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected eval status")
}

func TestBuildEvalCaseSummariesReturnsErrorOnEmptyEvalID(t *testing.T) {
	runCaseResults := map[int][]*evalresult.EvalCaseResult{
		1: {
			{EvalSetID: "set", EvalID: "", RunID: 1, FinalEvalStatus: status.EvalStatusPassed},
		},
	}

	_, _, err := buildEvalCaseSummaries(runCaseResults, []int{1})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "eval id is empty")
}

func TestBuildEvalCaseSummariesReturnsErrorOnUnexpectedFinalStatus(t *testing.T) {
	runCaseResults := map[int][]*evalresult.EvalCaseResult{
		1: {
			{EvalSetID: "set", EvalID: "A", RunID: 1, FinalEvalStatus: status.EvalStatusUnknown},
		},
	}

	_, _, err := buildEvalCaseSummaries(runCaseResults, []int{1})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected eval status")
}

func TestBuildEvalCaseSummariesSkipsNilCaseResults(t *testing.T) {
	runCaseResults := map[int][]*evalresult.EvalCaseResult{
		1: {
			nil,
			{EvalSetID: "set", EvalID: "A", RunID: 1, FinalEvalStatus: status.EvalStatusPassed},
		},
	}

	summaries, overall, err := buildEvalCaseSummaries(runCaseResults, []int{1})
	assert.NoError(t, err)
	assert.Equal(t, status.EvalStatusNotEvaluated, overall)
	assert.Len(t, summaries, 1)
	if len(summaries) == 1 && summaries[0] != nil {
		assert.Equal(t, "A", summaries[0].EvalID)
		assert.Equal(t, status.EvalStatusNotEvaluated, summaries[0].OverallStatus)
		assert.Len(t, summaries[0].RunSummaries, 1)
	}
}

func TestBuildEvalCaseSummariesReturnsErrorOnUnexpectedMetricStatus(t *testing.T) {
	runCaseResults := map[int][]*evalresult.EvalCaseResult{
		1: {
			{
				EvalSetID:       "set",
				EvalID:          "A",
				RunID:           1,
				FinalEvalStatus: status.EvalStatusPassed,
				OverallEvalMetricResults: []*evalresult.EvalMetricResult{
					{MetricName: "m", EvalStatus: status.EvalStatusUnknown},
				},
			},
		},
	}

	_, _, err := buildEvalCaseSummaries(runCaseResults, []int{1})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected eval status")
}

func TestSummarizeOverallFromMetricSummariesErrorsOnUnknownStatusAndSkipsNil(t *testing.T) {
	_, err := summarizeOverallFromMetricSummaries([]*evalresult.EvalMetricSummary{
		nil,
		{MetricName: "m", EvalStatus: status.EvalStatusUnknown},
	}, false)
	assert.Error(t, err)
}

func TestAddEvalStatusNilCountsReturnsError(t *testing.T) {
	err := addEvalStatus(nil, status.EvalStatusPassed)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "eval status counts is nil")
}

func TestMergeMetricAggSkipsNilMetricResultAndPreservesThreshold(t *testing.T) {
	agg := make(map[string]*metricAgg)
	err := mergeMetricAgg(agg, []*evalresult.EvalMetricResult{
		nil,
		{MetricName: "m", Threshold: 1, EvalStatus: status.EvalStatusPassed, Score: 1},
		{MetricName: "m", Threshold: 999, EvalStatus: status.EvalStatusPassed, Score: 1},
	})
	assert.NoError(t, err)
	assert.NotNil(t, agg["m"])
	if agg["m"] != nil {
		assert.Equal(t, 1.0, agg["m"].threshold)
		assert.Equal(t, 2, agg["m"].evaluatedCount)
	}
}

func TestBuildMetricSummariesSkipsNilMetricAggEntry(t *testing.T) {
	summaries := buildMetricSummaries(map[string]*metricAgg{"m": nil})
	assert.NotNil(t, summaries)
	assert.Len(t, summaries, 0)
}

func TestBuildMetricRunSummariesSkipsNilMetricResult(t *testing.T) {
	results := buildMetricRunSummaries([]*evalresult.EvalMetricResult{
		nil,
		{MetricName: "b", Score: 1, EvalStatus: status.EvalStatusPassed, Threshold: 1},
		{MetricName: "a", Score: 1, EvalStatus: status.EvalStatusPassed, Threshold: 1},
	})
	assert.Len(t, results, 2)
	if len(results) == 2 {
		assert.Equal(t, "a", results[0].MetricName)
		assert.Equal(t, "b", results[1].MetricName)
	}
}
