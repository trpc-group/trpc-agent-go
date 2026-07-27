//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestNormalizeEvaluationKeepsExecutionOnlyFailure(t *testing.T) {
	input := &evaluation.EvaluationResult{
		EvalSetID:     "validation",
		OverallStatus: status.EvalStatusFailed,
		EvalCases: []*evaluation.EvaluationCaseResult{{
			EvalCaseID:    "critical",
			OverallStatus: status.EvalStatusFailed,
			RunDetails: []*evaluation.EvaluationCaseRunDetails{{
				Inference: &evaluation.EvaluationInferenceDetails{
					Status:       status.EvalStatusFailed,
					ErrorMessage: "provider timeout",
				},
			}},
		}},
	}

	got, err := normalizeEvaluation(input, validationCatalog("critical", "quality"))
	require.NoError(t, err)
	require.Len(t, got.Cases, 1)
	require.Len(t, got.Cases[0].Metrics, 1)
	assert.Equal(t, status.EvalStatusFailed, got.Cases[0].Metrics[0].Status)
	assert.Equal(t, "provider timeout", got.Cases[0].Metrics[0].Reason)
	assert.Equal(t, "provider timeout", got.Cases[0].ExecutionError)
	assert.Zero(t, got.Score)
}

func TestNormalizeEvaluationKeepsFailedInferenceWithoutMessage(t *testing.T) {
	input := &evaluation.EvaluationResult{
		EvalSetID:     "validation",
		OverallStatus: status.EvalStatusFailed,
		EvalCases: []*evaluation.EvaluationCaseResult{{
			EvalCaseID:    "critical",
			OverallStatus: status.EvalStatusFailed,
			RunDetails: []*evaluation.EvaluationCaseRunDetails{{
				Inference: &evaluation.EvaluationInferenceDetails{
					Status: status.EvalStatusFailed,
				},
			}},
		}},
	}

	got, err := normalizeEvaluation(input, validationCatalog("critical", "quality"))
	require.NoError(t, err)
	assert.Equal(t, "inference failed", got.Cases[0].ExecutionError)
	assert.Equal(t, status.EvalStatusFailed, got.Cases[0].Metrics[0].Status)
}

func TestNormalizeEvaluationRejectsAggregateOnly(t *testing.T) {
	_, err := normalizeEvaluation(&evaluation.EvaluationResult{
		EvalSetID: "validation",
		EvalResult: &evalresult.EvalSetResult{
			EvalSetID: "validation",
			EvalCaseResults: []*evalresult.EvalCaseResult{{
				EvalSetID: "validation",
				EvalID:    "critical",
			}},
		},
	}, validationCatalog("critical", "quality"))
	require.ErrorContains(t, err, "missing case")
}

func TestNormalizeEvaluationRejectsIncompleteAndWrongShape(t *testing.T) {
	tests := []struct {
		name    string
		result  *evaluation.EvaluationResult
		message string
	}{
		{
			name: "not evaluated metric",
			result: evaluationWithMetrics("validation", "critical",
				testMetric("quality", 0, status.EvalStatusNotEvaluated, "")),
			message: "not_evaluated",
		},
		{
			name: "unknown overall status",
			result: func() *evaluation.EvaluationResult {
				result := evaluationWithMetrics("validation", "critical",
					testMetric("quality", 1, status.EvalStatusPassed, ""))
				result.OverallStatus = status.EvalStatusUnknown
				return result
			}(),
			message: "unknown",
		},
		{
			name: "duplicate case",
			result: func() *evaluation.EvaluationResult {
				result := evaluationWithMetrics("validation", "critical",
					testMetric("quality", 1, status.EvalStatusPassed, ""))
				result.EvalCases = append(result.EvalCases, result.EvalCases[0])
				return result
			}(),
			message: "duplicate case",
		},
		{
			name: "extra metric",
			result: evaluationWithMetrics("validation", "critical",
				testMetric("quality", 1, status.EvalStatusPassed, ""),
				testMetric("unexpected", 1, status.EvalStatusPassed, "")),
			message: "unexpected metric",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeEvaluation(tt.result, validationCatalog("critical", "quality"))
			require.ErrorContains(t, err, tt.message)
		})
	}
}

func TestNormalizeEvaluationPrefersRetainedReasonAndComputesScore(t *testing.T) {
	result := evaluationWithMetrics(
		"validation",
		"critical",
		testMetric("quality", 0.8, status.EvalStatusPassed, "aggregate reason"),
		testMetric("format", 0.4, status.EvalStatusFailed, "format aggregate"),
	)
	result.EvalCases[0].EvalCaseResults = []*evalresult.EvalCaseResult{{
		EvalSetID: "validation",
		EvalID:    "critical",
		OverallEvalMetricResults: []*evalresult.EvalMetricResult{
			testMetric("quality", 0.8, status.EvalStatusPassed, "retained reason"),
			testMetric("format", 0.4, status.EvalStatusFailed, ""),
		},
	}}

	got, err := normalizeEvaluation(result, validationCatalog("critical", "quality", "format"))
	require.NoError(t, err)
	require.Len(t, got.Cases, 1)
	assert.InDelta(t, 0.6, got.Cases[0].Score, 1e-12)
	assert.InDelta(t, 0.6, got.Score, 1e-12)
	assert.Equal(t, "retained reason", got.Cases[0].Metrics[0].Reason)
	assert.Equal(t, "format aggregate", got.Cases[0].Metrics[1].Reason)
}

func validationCatalog(caseID string, metricNames ...string) *catalog {
	keys := make(map[resultKey]struct{}, len(metricNames))
	for _, name := range metricNames {
		keys[resultKey{EvalSetID: "validation", EvalCaseID: caseID, MetricName: name}] = struct{}{}
	}
	return &catalog{
		EvalSetID:   "validation",
		EvalCaseIDs: []string{caseID},
		MetricNames: metricNames,
		ResultKeys:  keys,
	}
}

func evaluationWithMetrics(evalSetID, caseID string, metrics ...*evalresult.EvalMetricResult) *evaluation.EvaluationResult {
	return &evaluation.EvaluationResult{
		EvalSetID:     evalSetID,
		OverallStatus: status.EvalStatusFailed,
		EvalCases: []*evaluation.EvaluationCaseResult{{
			EvalCaseID:    caseID,
			OverallStatus: status.EvalStatusFailed,
			MetricResults: metrics,
		}},
	}
}

func testMetric(name string, score float64, evalStatus status.EvalStatus, reason string) *evalresult.EvalMetricResult {
	return &evalresult.EvalMetricResult{
		MetricName: name,
		Score:      score,
		EvalStatus: evalStatus,
		Details: &evalresult.EvalMetricResultDetails{
			Reason: reason,
		},
	}
}
