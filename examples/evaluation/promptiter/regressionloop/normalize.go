//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func normalizeEvaluation(input *evaluation.EvaluationResult, expected *catalog) (evaluationSnapshot, error) {
	if input == nil {
		return evaluationSnapshot{}, errors.New("evaluation result is nil")
	}
	if expected == nil {
		return evaluationSnapshot{}, errors.New("evaluation catalog is nil")
	}
	if input.EvalSetID != expected.EvalSetID {
		return evaluationSnapshot{}, fmt.Errorf(
			"evaluation set ID %q does not match expected %q",
			input.EvalSetID,
			expected.EvalSetID,
		)
	}
	caseByID := make(map[string]*evaluation.EvaluationCaseResult, len(input.EvalCases))
	for i, evalCase := range input.EvalCases {
		if evalCase == nil {
			return evaluationSnapshot{}, fmt.Errorf("null case at index %d", i)
		}
		if _, ok := caseByID[evalCase.EvalCaseID]; ok {
			return evaluationSnapshot{}, fmt.Errorf("duplicate case %q", evalCase.EvalCaseID)
		}
		if !contains(expected.EvalCaseIDs, evalCase.EvalCaseID) {
			return evaluationSnapshot{}, fmt.Errorf("unexpected case %q", evalCase.EvalCaseID)
		}
		caseByID[evalCase.EvalCaseID] = evalCase
	}
	for _, caseID := range expected.EvalCaseIDs {
		if _, ok := caseByID[caseID]; !ok {
			return evaluationSnapshot{}, fmt.Errorf("missing case %q", caseID)
		}
	}
	if err := validateTerminalStatus("evaluation", input.OverallStatus); err != nil {
		return evaluationSnapshot{}, err
	}

	snapshot := evaluationSnapshot{
		EvalSetID: input.EvalSetID,
		Status:    input.OverallStatus,
		Cases:     make([]caseResult, 0, len(expected.EvalCaseIDs)),
		Duration:  input.ExecutionTime,
	}
	var scoreSum float64
	var scoreCount int
	for _, caseID := range expected.EvalCaseIDs {
		evalCase := caseByID[caseID]
		normalized, err := normalizeCase(input.EvalSetID, evalCase, expected)
		if err != nil {
			return evaluationSnapshot{}, fmt.Errorf("normalize case %q: %w", caseID, err)
		}
		for _, metric := range normalized.Metrics {
			scoreSum += metric.Score
			scoreCount++
		}
		snapshot.Cases = append(snapshot.Cases, normalized)
	}
	if scoreCount == 0 {
		return evaluationSnapshot{}, errors.New("evaluation has no metric evidence")
	}
	snapshot.Score = scoreSum / float64(scoreCount)
	return snapshot, nil
}

func normalizeCase(evalSetID string, input *evaluation.EvaluationCaseResult, expected *catalog) (caseResult, error) {
	if err := validateTerminalStatus("case", input.OverallStatus); err != nil {
		return caseResult{}, err
	}
	executionError := caseExecutionError(input)
	detailedReasons, metricEvidence, err := retainedMetricEvidence(
		evalSetID,
		input.EvalCaseID,
		input.EvalCaseResults,
		expected,
	)
	if err != nil {
		return caseResult{}, err
	}
	aggregates, err := aggregateMetrics(input.MetricResults, expected)
	if err != nil {
		return caseResult{}, err
	}

	result := caseResult{
		EvalSetID:      evalSetID,
		EvalCaseID:     input.EvalCaseID,
		Status:         input.OverallStatus,
		ExecutionError: executionError,
		Metrics:        make([]metricResult, 0, len(expected.MetricNames)),
		MetricEvidence: metricEvidence,
		RunDetails:     input.RunDetails,
	}
	var scoreSum float64
	for _, metricName := range expected.MetricNames {
		aggregate, ok := aggregates[metricName]
		if !ok {
			if executionError == "" {
				return caseResult{}, fmt.Errorf("missing metric %q", metricName)
			}
			result.Metrics = append(result.Metrics, metricResult{
				Name:   metricName,
				Status: status.EvalStatusFailed,
				Reason: executionError,
			})
			continue
		}
		if err := validateTerminalStatus("metric "+metricName, aggregate.EvalStatus); err != nil {
			return caseResult{}, err
		}
		reason := detailedReasons[metricName]
		if reason == "" && aggregate.Details != nil {
			reason = aggregate.Details.Reason
		}
		result.Metrics = append(result.Metrics, metricResult{
			Name:      metricName,
			Score:     aggregate.Score,
			Threshold: aggregate.Threshold,
			Status:    aggregate.EvalStatus,
			Reason:    reason,
		})
		scoreSum += aggregate.Score
	}
	result.Score = scoreSum / float64(len(expected.MetricNames))
	return result, nil
}

func aggregateMetrics(metrics []*evalresult.EvalMetricResult, expected *catalog) (map[string]*evalresult.EvalMetricResult, error) {
	result := make(map[string]*evalresult.EvalMetricResult, len(metrics))
	for i, metric := range metrics {
		if metric == nil {
			return nil, fmt.Errorf("null metric at index %d", i)
		}
		if !contains(expected.MetricNames, metric.MetricName) {
			return nil, fmt.Errorf("unexpected metric %q", metric.MetricName)
		}
		if _, ok := result[metric.MetricName]; ok {
			return nil, fmt.Errorf("duplicate metric %q", metric.MetricName)
		}
		result[metric.MetricName] = metric
	}
	return result, nil
}

// retainedMetricEvidence uses the first run as the stable diagnostic sample;
// aggregate metrics remain authoritative for scoring and shape validation.
func retainedMetricEvidence(
	evalSetID string,
	caseID string,
	results []*evalresult.EvalCaseResult,
	expected *catalog,
) (map[string]string, []*evalresult.EvalMetricResultPerInvocation, error) {
	reasons := make(map[string]string)
	if len(results) == 0 {
		return reasons, nil, nil
	}
	retained := results[0]
	if retained == nil {
		return nil, nil, errors.New("retained case result is null")
	}
	if retained.EvalSetID != "" && retained.EvalSetID != evalSetID {
		return nil, nil, fmt.Errorf("retained evaluation set ID %q does not match %q", retained.EvalSetID, evalSetID)
	}
	if retained.EvalID != "" && retained.EvalID != caseID {
		return nil, nil, fmt.Errorf("retained case ID %q does not match %q", retained.EvalID, caseID)
	}
	for i, metric := range retained.OverallEvalMetricResults {
		if metric == nil {
			return nil, nil, fmt.Errorf("null retained metric at index %d", i)
		}
		if !contains(expected.MetricNames, metric.MetricName) {
			return nil, nil, fmt.Errorf("unexpected retained metric %q", metric.MetricName)
		}
		if _, ok := reasons[metric.MetricName]; ok {
			return nil, nil, fmt.Errorf("duplicate retained metric %q", metric.MetricName)
		}
		if metric.Details == nil {
			reasons[metric.MetricName] = ""
			continue
		}
		reasons[metric.MetricName] = metric.Details.Reason
	}
	return reasons, retained.EvalMetricResultPerInvocation, nil
}

// caseExecutionError prefers explicit run errors, then explicit inference
// errors, and finally a failed inference status. A failed case status alone can
// be an ordinary metric verdict and is not evidence of an execution failure.
func caseExecutionError(evalCase *evaluation.EvaluationCaseResult) string {
	for _, result := range evalCase.EvalCaseResults {
		if result != nil && strings.TrimSpace(result.ErrorMessage) != "" {
			return result.ErrorMessage
		}
	}
	for _, detail := range evalCase.RunDetails {
		if detail != nil && detail.Inference != nil && strings.TrimSpace(detail.Inference.ErrorMessage) != "" {
			return detail.Inference.ErrorMessage
		}
	}
	for _, detail := range evalCase.RunDetails {
		if detail != nil && detail.Inference != nil && detail.Inference.Status == status.EvalStatusFailed {
			return "inference failed"
		}
	}
	return ""
}

func validateTerminalStatus(subject string, value status.EvalStatus) error {
	if value != status.EvalStatusPassed && value != status.EvalStatusFailed {
		return fmt.Errorf("%s status is %s", subject, value)
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
