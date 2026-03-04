//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clone

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
)

// CloneEvalSetResult clones an evaluation result set.
func CloneEvalSetResult(src *evalresult.EvalSetResult) (*evalresult.EvalSetResult, error) {
	if src == nil {
		return nil, errNilInput("eval set result")
	}
	copied := *src
	copied.CreationTimestamp = cloneEpochTime(src.CreationTimestamp)
	caseResults, err := cloneEvalCaseResults(src.EvalCaseResults)
	if err != nil {
		return nil, err
	}
	copied.EvalCaseResults = caseResults
	copied.Summary = cloneEvalSetResultSummary(src.Summary)
	return &copied, nil
}

func cloneEvalCaseResults(src []*evalresult.EvalCaseResult) ([]*evalresult.EvalCaseResult, error) {
	if src == nil {
		return nil, nil
	}
	copied := make([]*evalresult.EvalCaseResult, len(src))
	for i := range src {
		caseResult, err := cloneEvalCaseResult(src[i])
		if err != nil {
			return nil, err
		}
		copied[i] = caseResult
	}
	return copied, nil
}

func cloneEvalCaseResult(src *evalresult.EvalCaseResult) (*evalresult.EvalCaseResult, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	overallMetrics, err := cloneEvalMetricResults(src.OverallEvalMetricResults)
	if err != nil {
		return nil, err
	}
	copied.OverallEvalMetricResults = overallMetrics
	perInvocationMetrics, err := cloneEvalMetricResultsPerInvocation(src.EvalMetricResultPerInvocation)
	if err != nil {
		return nil, err
	}
	copied.EvalMetricResultPerInvocation = perInvocationMetrics
	return &copied, nil
}

func cloneEvalMetricResults(src []*evalresult.EvalMetricResult) ([]*evalresult.EvalMetricResult, error) {
	if src == nil {
		return nil, nil
	}
	copied := make([]*evalresult.EvalMetricResult, len(src))
	for i := range src {
		metricResult, err := cloneEvalMetricResult(src[i])
		if err != nil {
			return nil, err
		}
		copied[i] = metricResult
	}
	return copied, nil
}

func cloneEvalMetricResult(src *evalresult.EvalMetricResult) (*evalresult.EvalMetricResult, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	clonedCriterion, err := cloneCriterion(src.Criterion)
	if err != nil {
		return nil, err
	}
	copied.Criterion = clonedCriterion
	copied.Details = cloneEvalMetricResultDetails(src.Details)
	return &copied, nil
}

func cloneEvalMetricResultDetails(src *evalresult.EvalMetricResultDetails) *evalresult.EvalMetricResultDetails {
	if src == nil {
		return nil
	}
	copied := *src
	copied.RubricScores = cloneRubricScores(src.RubricScores)
	return &copied
}

func cloneRubricScores(src []*evalresult.RubricScore) []*evalresult.RubricScore {
	if src == nil {
		return nil
	}
	copied := make([]*evalresult.RubricScore, len(src))
	for i := range src {
		if src[i] == nil {
			continue
		}
		score := *src[i]
		copied[i] = &score
	}
	return copied
}

func cloneEvalMetricResultsPerInvocation(src []*evalresult.EvalMetricResultPerInvocation) ([]*evalresult.EvalMetricResultPerInvocation, error) {
	if src == nil {
		return nil, nil
	}
	copied := make([]*evalresult.EvalMetricResultPerInvocation, len(src))
	for i := range src {
		perInvocation, err := cloneEvalMetricResultPerInvocation(src[i])
		if err != nil {
			return nil, err
		}
		copied[i] = perInvocation
	}
	return copied, nil
}

func cloneEvalMetricResultPerInvocation(src *evalresult.EvalMetricResultPerInvocation) (*evalresult.EvalMetricResultPerInvocation, error) {
	if src == nil {
		return nil, nil
	}
	copied := *src
	actualInvocation, err := cloneInvocation(src.ActualInvocation)
	if err != nil {
		return nil, err
	}
	copied.ActualInvocation = actualInvocation
	expectedInvocation, err := cloneInvocation(src.ExpectedInvocation)
	if err != nil {
		return nil, err
	}
	copied.ExpectedInvocation = expectedInvocation
	metricResults, err := cloneEvalMetricResults(src.EvalMetricResults)
	if err != nil {
		return nil, err
	}
	copied.EvalMetricResults = metricResults
	return &copied, nil
}

func cloneEvalSetResultSummary(src *evalresult.EvalSetResultSummary) *evalresult.EvalSetResultSummary {
	if src == nil {
		return nil
	}
	copied := *src
	copied.RunStatusCounts = cloneEvalStatusCounts(src.RunStatusCounts)
	copied.RunSummaries = cloneEvalSetRunSummaries(src.RunSummaries)
	copied.EvalCaseSummaries = cloneEvalCaseResultSummaries(src.EvalCaseSummaries)
	return &copied
}

func cloneEvalSetRunSummaries(src []*evalresult.EvalSetRunSummary) []*evalresult.EvalSetRunSummary {
	if src == nil {
		return nil
	}
	copied := make([]*evalresult.EvalSetRunSummary, len(src))
	for i := range src {
		copied[i] = cloneEvalSetRunSummary(src[i])
	}
	return copied
}

func cloneEvalSetRunSummary(src *evalresult.EvalSetRunSummary) *evalresult.EvalSetRunSummary {
	if src == nil {
		return nil
	}
	copied := *src
	copied.CaseStatusCounts = cloneEvalStatusCounts(src.CaseStatusCounts)
	copied.MetricSummaries = cloneEvalMetricSummaries(src.MetricSummaries)
	return &copied
}

func cloneEvalCaseResultSummaries(src []*evalresult.EvalCaseResultSummary) []*evalresult.EvalCaseResultSummary {
	if src == nil {
		return nil
	}
	copied := make([]*evalresult.EvalCaseResultSummary, len(src))
	for i := range src {
		copied[i] = cloneEvalCaseResultSummary(src[i])
	}
	return copied
}

func cloneEvalCaseResultSummary(src *evalresult.EvalCaseResultSummary) *evalresult.EvalCaseResultSummary {
	if src == nil {
		return nil
	}
	copied := *src
	copied.RunStatusCounts = cloneEvalStatusCounts(src.RunStatusCounts)
	copied.MetricSummaries = cloneEvalMetricSummaries(src.MetricSummaries)
	copied.RunSummaries = cloneEvalCaseRunSummaries(src.RunSummaries)
	return &copied
}

func cloneEvalCaseRunSummaries(src []*evalresult.EvalCaseRunSummary) []*evalresult.EvalCaseRunSummary {
	if src == nil {
		return nil
	}
	copied := make([]*evalresult.EvalCaseRunSummary, len(src))
	for i := range src {
		copied[i] = cloneEvalCaseRunSummary(src[i])
	}
	return copied
}

func cloneEvalCaseRunSummary(src *evalresult.EvalCaseRunSummary) *evalresult.EvalCaseRunSummary {
	if src == nil {
		return nil
	}
	copied := *src
	copied.MetricResults = cloneEvalMetricRunSummaries(src.MetricResults)
	return &copied
}

func cloneEvalMetricRunSummaries(src []*evalresult.EvalMetricRunSummary) []*evalresult.EvalMetricRunSummary {
	if src == nil {
		return nil
	}
	copied := make([]*evalresult.EvalMetricRunSummary, len(src))
	for i := range src {
		if src[i] == nil {
			continue
		}
		v := *src[i]
		copied[i] = &v
	}
	return copied
}

func cloneEvalMetricSummaries(src []*evalresult.EvalMetricSummary) []*evalresult.EvalMetricSummary {
	if src == nil {
		return nil
	}
	copied := make([]*evalresult.EvalMetricSummary, len(src))
	for i := range src {
		copied[i] = cloneEvalMetricSummary(src[i])
	}
	return copied
}

func cloneEvalMetricSummary(src *evalresult.EvalMetricSummary) *evalresult.EvalMetricSummary {
	if src == nil {
		return nil
	}
	copied := *src
	copied.StatusCounts = cloneEvalStatusCounts(src.StatusCounts)
	return &copied
}

func cloneEvalStatusCounts(src *evalresult.EvalStatusCounts) *evalresult.EvalStatusCounts {
	if src == nil {
		return nil
	}
	copied := *src
	return &copied
}
