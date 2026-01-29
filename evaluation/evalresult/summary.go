//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evalresult

import "trpc.group/trpc-go/trpc-agent-go/evaluation/status"

// EvalSetResultSummary summarizes a multi-run eval set result for easier inspection.
type EvalSetResultSummary struct {
	// OverallStatus summarizes the aggregated evaluation status across all cases and runs.
	OverallStatus status.EvalStatus `json:"overallStatus,omitempty"`
	// NumRuns is the number of eval set runs contained in this result.
	NumRuns int `json:"numRuns,omitempty"`
	// RunStatusCounts counts the overall status of each eval set run.
	RunStatusCounts *EvalStatusCounts `json:"runStatusCounts,omitempty"`
	// RunSummaries contains summaries for each eval set run.
	RunSummaries []*EvalSetRunSummary `json:"runSummaries,omitempty"`
	// EvalCaseSummaries contains summaries for each eval case across runs.
	EvalCaseSummaries []*EvalCaseResultSummary `json:"evalCaseSummaries,omitempty"`
}

// EvalSetRunSummary summarizes a single eval set run.
type EvalSetRunSummary struct {
	// RunID identifies the eval set run within the result.
	RunID int `json:"runId,omitempty"`
	// OverallStatus summarizes the evaluation status for this run across all eval cases.
	OverallStatus status.EvalStatus `json:"overallStatus,omitempty"`
	// CaseStatusCounts counts final statuses of eval cases in this run.
	CaseStatusCounts *EvalStatusCounts `json:"caseStatusCounts,omitempty"`
	// MetricSummaries contains aggregated metric outcomes across all cases in this run.
	MetricSummaries []*EvalMetricSummary `json:"metricSummaries,omitempty"`
}

// EvalCaseResultSummary summarizes a single eval case across multiple runs.
type EvalCaseResultSummary struct {
	// EvalID identifies the eval case.
	EvalID string `json:"evalId,omitempty"`
	// OverallStatus summarizes the aggregated evaluation status for this case across runs.
	OverallStatus status.EvalStatus `json:"overallStatus,omitempty"`
	// RunStatusCounts counts final statuses of this eval case across runs.
	RunStatusCounts *EvalStatusCounts `json:"runStatusCounts,omitempty"`
	// MetricSummaries contains aggregated metric outcomes across runs.
	MetricSummaries []*EvalMetricSummary `json:"metricSummaries,omitempty"`
	// RunSummaries contains per-run summaries for this eval case.
	RunSummaries []*EvalCaseRunSummary `json:"runSummaries,omitempty"`
}

// EvalCaseRunSummary summarizes a single run of an eval case.
type EvalCaseRunSummary struct {
	// RunID identifies the run within the eval set result.
	RunID int `json:"runId,omitempty"`
	// FinalEvalStatus is the final eval status for this run.
	FinalEvalStatus status.EvalStatus `json:"finalEvalStatus,omitempty"`
	// ErrorMessage contains the error message when evaluation execution failed.
	ErrorMessage string `json:"errorMessage,omitempty"`
	// MetricResults contains overall metric outcomes for this run.
	MetricResults []*EvalMetricRunSummary `json:"metricResults,omitempty"`
}

// EvalMetricRunSummary summarizes a metric result in a single run.
type EvalMetricRunSummary struct {
	// MetricName identifies the metric.
	MetricName string `json:"metricName,omitempty"`
	// Score is the overall score for this metric in the run.
	Score float64 `json:"score,omitempty"`
	// EvalStatus is the status of this metric evaluation.
	EvalStatus status.EvalStatus `json:"evalStatus,omitempty"`
	// Threshold is the threshold that was used.
	Threshold float64 `json:"threshold,omitempty"`
}

// EvalMetricSummary summarizes metric results across a collection of samples.
type EvalMetricSummary struct {
	// MetricName identifies the metric.
	MetricName string `json:"metricName,omitempty"`
	// AverageScore is the averaged score across samples that were evaluated.
	AverageScore float64 `json:"averageScore,omitempty"`
	// EvalStatus is the aggregated status derived from the averaged score and threshold.
	EvalStatus status.EvalStatus `json:"evalStatus,omitempty"`
	// Threshold is the threshold that was used.
	Threshold float64 `json:"threshold,omitempty"`
	// StatusCounts counts metric statuses across samples.
	StatusCounts *EvalStatusCounts `json:"statusCounts,omitempty"`
}

// EvalStatusCounts records a simple histogram of evaluation statuses.
type EvalStatusCounts struct {
	// Passed is the count of passed statuses.
	Passed int `json:"passed,omitempty"`
	// Failed is the count of failed statuses.
	Failed int `json:"failed,omitempty"`
	// NotEvaluated is the count of not evaluated statuses.
	NotEvaluated int `json:"notEvaluated,omitempty"`
}
