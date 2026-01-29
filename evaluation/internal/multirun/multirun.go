//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package multirun provides helpers for summarizing multi-run evaluation results.
package multirun

import (
	"errors"
	"fmt"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	istatus "trpc.group/trpc-go/trpc-agent-go/evaluation/internal/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// SummarizeMultiRun populates EvalSetResult.Summary based on the current EvalCaseResults.
func SummarizeMultiRun(evalSetResult *evalresult.EvalSetResult, expectedNumRuns int) error {
	if evalSetResult == nil {
		return errors.New("eval set result is nil")
	}
	runCaseResults, numRuns, err := groupCaseResultsByRunID(evalSetResult.EvalCaseResults, expectedNumRuns)
	if err != nil {
		return err
	}
	runIDs := make([]int, 0, numRuns)
	for runID := 1; runID <= numRuns; runID++ {
		runIDs = append(runIDs, runID)
	}
	runSummaries, runStatusCounts, err := buildEvalSetRunSummaries(runCaseResults, runIDs)
	if err != nil {
		return err
	}
	caseSummaries, overallStatus, err := buildEvalCaseSummaries(runCaseResults, runIDs)
	if err != nil {
		return err
	}
	evalSetResult.Summary = &evalresult.EvalSetResultSummary{
		OverallStatus:     overallStatus,
		NumRuns:           numRuns,
		RunSummaries:      runSummaries,
		RunStatusCounts:   normalizeCounts(runStatusCounts),
		EvalCaseSummaries: caseSummaries,
	}
	return nil
}

func groupCaseResultsByRunID(caseResults []*evalresult.EvalCaseResult, expectedNumRuns int) (map[int][]*evalresult.EvalCaseResult, int, error) {
	if expectedNumRuns < 0 {
		return nil, 0, fmt.Errorf("expected num runs is negative: %d", expectedNumRuns)
	}
	runCaseResults := make(map[int][]*evalresult.EvalCaseResult)
	nonNilCount := 0
	maxRunID := 0
	for idx, caseResult := range caseResults {
		if caseResult == nil {
			continue
		}
		nonNilCount++
		if caseResult.EvalID == "" {
			return nil, 0, fmt.Errorf("eval id at index %d is empty", idx)
		}
		runID := caseResult.RunID
		if runID <= 0 {
			return nil, 0, fmt.Errorf("run id at index %d is not set", idx)
		}
		if expectedNumRuns > 0 && runID > expectedNumRuns {
			return nil, 0, fmt.Errorf("run id %d at index %d exceeds expected num runs %d", runID, idx, expectedNumRuns)
		}
		if runID > maxRunID {
			maxRunID = runID
		}
		runCaseResults[runID] = append(runCaseResults[runID], caseResult)
	}
	if nonNilCount == 0 {
		maxRunID = expectedNumRuns
	}
	numRuns := maxRunID
	if expectedNumRuns > 0 {
		numRuns = expectedNumRuns
	}
	if numRuns <= 0 {
		numRuns = 1
	}
	return runCaseResults, numRuns, nil
}

func buildEvalSetRunSummaries(runCaseResults map[int][]*evalresult.EvalCaseResult, runIDs []int) ([]*evalresult.EvalSetRunSummary, evalresult.EvalStatusCounts, error) {
	if len(runIDs) == 0 {
		return nil, evalresult.EvalStatusCounts{}, nil
	}
	summaries := make([]*evalresult.EvalSetRunSummary, 0, len(runIDs))
	var runStatusCounts evalresult.EvalStatusCounts
	for _, runID := range runIDs {
		var statuses []status.EvalStatus
		var caseStatusCounts evalresult.EvalStatusCounts
		metricAgg := make(map[string]*metricAgg)
		for _, caseResult := range runCaseResults[runID] {
			if caseResult == nil {
				continue
			}
			statuses = append(statuses, caseResult.FinalEvalStatus)
			if err := addEvalStatus(&caseStatusCounts, caseResult.FinalEvalStatus); err != nil {
				return nil, evalresult.EvalStatusCounts{}, err
			}
			if err := mergeMetricAgg(metricAgg, caseResult.OverallEvalMetricResults); err != nil {
				return nil, evalresult.EvalStatusCounts{}, err
			}
		}
		overallStatus, err := istatus.Summarize(statuses)
		if err != nil {
			return nil, evalresult.EvalStatusCounts{}, fmt.Errorf("summarize run %d status: %w", runID, err)
		}
		if err := addEvalStatus(&runStatusCounts, overallStatus); err != nil {
			return nil, evalresult.EvalStatusCounts{}, err
		}
		summaries = append(summaries, &evalresult.EvalSetRunSummary{
			RunID:            runID,
			OverallStatus:    overallStatus,
			CaseStatusCounts: normalizeCounts(caseStatusCounts),
			MetricSummaries:  buildMetricSummaries(metricAgg),
		})
	}
	return summaries, runStatusCounts, nil
}

type caseAgg struct {
	runStatusCounts evalresult.EvalStatusCounts
	hasRunError     bool
	runSummaries    []*evalresult.EvalCaseRunSummary
	metricAgg       map[string]*metricAgg
}

func buildEvalCaseSummaries(runCaseResults map[int][]*evalresult.EvalCaseResult, runIDs []int) ([]*evalresult.EvalCaseResultSummary, status.EvalStatus, error) {
	aggs := make(map[string]*caseAgg)
	for _, runID := range runIDs {
		for _, caseResult := range runCaseResults[runID] {
			if caseResult == nil {
				continue
			}
			caseID := caseResult.EvalID
			if caseID == "" {
				return nil, status.EvalStatusFailed, errors.New("eval id is empty")
			}
			agg := aggs[caseID]
			if agg == nil {
				agg = &caseAgg{metricAgg: make(map[string]*metricAgg)}
				aggs[caseID] = agg
			}
			if caseResult.ErrorMessage != "" {
				agg.hasRunError = true
			}
			if err := addEvalStatus(&agg.runStatusCounts, caseResult.FinalEvalStatus); err != nil {
				return nil, status.EvalStatusFailed, err
			}
			agg.runSummaries = append(agg.runSummaries, &evalresult.EvalCaseRunSummary{
				RunID:           runID,
				FinalEvalStatus: caseResult.FinalEvalStatus,
				ErrorMessage:    caseResult.ErrorMessage,
				MetricResults:   buildMetricRunSummaries(caseResult.OverallEvalMetricResults),
			})
			if err := mergeMetricAgg(agg.metricAgg, caseResult.OverallEvalMetricResults); err != nil {
				return nil, status.EvalStatusFailed, err
			}
		}
	}

	caseSummaries := make([]*evalresult.EvalCaseResultSummary, 0, len(aggs))
	caseStatuses := make([]status.EvalStatus, 0, len(aggs))
	for caseID, agg := range aggs {
		metricSummaries := buildMetricSummaries(agg.metricAgg)
		overallStatus, err := summarizeOverallFromMetricSummaries(metricSummaries, agg.hasRunError)
		if err != nil {
			return nil, status.EvalStatusFailed, fmt.Errorf("summarize case %s status: %w", caseID, err)
		}
		caseStatuses = append(caseStatuses, overallStatus)
		caseSummaries = append(caseSummaries, &evalresult.EvalCaseResultSummary{
			EvalID:          caseID,
			OverallStatus:   overallStatus,
			RunStatusCounts: normalizeCounts(agg.runStatusCounts),
			MetricSummaries: metricSummaries,
			RunSummaries:    agg.runSummaries,
		})
	}
	sort.SliceStable(caseSummaries, func(i, j int) bool {
		return caseSummaries[i].EvalID < caseSummaries[j].EvalID
	})
	overallStatus, err := istatus.Summarize(caseStatuses)
	if err != nil {
		return nil, status.EvalStatusFailed, fmt.Errorf("summarize eval set status: %w", err)
	}
	return caseSummaries, overallStatus, nil
}

func summarizeOverallFromMetricSummaries(metricSummaries []*evalresult.EvalMetricSummary, hasRunError bool) (status.EvalStatus, error) {
	statuses := make([]status.EvalStatus, 0, len(metricSummaries))
	for _, summary := range metricSummaries {
		if summary == nil {
			continue
		}
		statuses = append(statuses, summary.EvalStatus)
	}
	overallStatus, err := istatus.Summarize(statuses)
	if err != nil {
		return status.EvalStatusFailed, err
	}
	if overallStatus == status.EvalStatusNotEvaluated && hasRunError {
		return status.EvalStatusFailed, nil
	}
	return overallStatus, nil
}

func addEvalStatus(counts *evalresult.EvalStatusCounts, s status.EvalStatus) error {
	if counts == nil {
		return errors.New("eval status counts is nil")
	}
	switch s {
	case status.EvalStatusPassed:
		counts.Passed++
	case status.EvalStatusFailed:
		counts.Failed++
	case status.EvalStatusNotEvaluated:
		counts.NotEvaluated++
	default:
		return fmt.Errorf("unexpected eval status %v", s)
	}
	return nil
}

func normalizeCounts(counts evalresult.EvalStatusCounts) *evalresult.EvalStatusCounts {
	if counts.Passed == 0 && counts.Failed == 0 && counts.NotEvaluated == 0 {
		return nil
	}
	copied := counts
	return &copied
}

type metricAgg struct {
	threshold       float64
	thresholdLoaded bool
	evaluatedCount  int
	scoreSum        float64
	statusCounts    evalresult.EvalStatusCounts
}

func mergeMetricAgg(agg map[string]*metricAgg, metricResults []*evalresult.EvalMetricResult) error {
	for _, metricResult := range metricResults {
		if metricResult == nil {
			continue
		}
		m := agg[metricResult.MetricName]
		if m == nil {
			m = &metricAgg{}
			agg[metricResult.MetricName] = m
		}
		if !m.thresholdLoaded {
			m.threshold = metricResult.Threshold
			m.thresholdLoaded = true
		}
		if err := addEvalStatus(&m.statusCounts, metricResult.EvalStatus); err != nil {
			return err
		}
		if metricResult.EvalStatus == status.EvalStatusNotEvaluated {
			continue
		}
		m.evaluatedCount++
		m.scoreSum += metricResult.Score
	}
	return nil
}

func buildMetricSummaries(agg map[string]*metricAgg) []*evalresult.EvalMetricSummary {
	if len(agg) == 0 {
		return nil
	}
	names := make([]string, 0, len(agg))
	for name := range agg {
		names = append(names, name)
	}
	sort.Strings(names)
	summaries := make([]*evalresult.EvalMetricSummary, 0, len(names))
	for _, name := range names {
		m := agg[name]
		if m == nil {
			continue
		}
		averageScore := 0.0
		evalStatus := status.EvalStatusNotEvaluated
		if m.evaluatedCount > 0 {
			averageScore = m.scoreSum / float64(m.evaluatedCount)
			evalStatus = status.EvalStatusFailed
			if averageScore >= m.threshold {
				evalStatus = status.EvalStatusPassed
			}
		}
		summaries = append(summaries, &evalresult.EvalMetricSummary{
			MetricName:   name,
			AverageScore: averageScore,
			EvalStatus:   evalStatus,
			Threshold:    m.threshold,
			StatusCounts: normalizeCounts(m.statusCounts),
		})
	}
	return summaries
}

func buildMetricRunSummaries(metricResults []*evalresult.EvalMetricResult) []*evalresult.EvalMetricRunSummary {
	if len(metricResults) == 0 {
		return nil
	}
	results := make([]*evalresult.EvalMetricRunSummary, 0, len(metricResults))
	for _, metricResult := range metricResults {
		if metricResult == nil {
			continue
		}
		results = append(results, &evalresult.EvalMetricRunSummary{
			MetricName: metricResult.MetricName,
			Score:      metricResult.Score,
			EvalStatus: metricResult.EvalStatus,
			Threshold:  metricResult.Threshold,
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].MetricName < results[j].MetricName
	})
	return results
}
