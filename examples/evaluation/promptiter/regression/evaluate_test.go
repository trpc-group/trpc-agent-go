//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestAdaptEvaluationRejectsMixedExecutionFailure(t *testing.T) {
	tests := []struct {
		name       string
		failedCase *evaluation.EvaluationCaseResult
		wantError  string
	}{
		{
			name: "inference failure",
			failedCase: evaluationCaseResult(
				"failed",
				status.EvalStatusFailed,
				"runner failed",
				"",
			),
			wantError: "runner failed",
		},
		{
			name: "evaluation failure",
			failedCase: evaluationCaseResult(
				"failed",
				status.EvalStatusPassed,
				"",
				"metric evaluation failed",
			),
			wantError: "metric evaluation failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &evaluationRuntime{}
			_, err := runtime.adaptEvaluation(
				&evaluation.EvaluationResult{
					EvalSetID: "validation",
					EvalCases: []*evaluation.EvaluationCaseResult{
						successfulEvaluationCaseResult("healthy"),
						test.failedCase,
					},
				},
				&evalset.EvalSet{
					EvalSetID: "validation",
					EvalCases: []*evalset.EvalCase{
						{EvalID: "healthy"},
						{EvalID: "failed"},
					},
				},
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf(
					"adaptEvaluation error = %v, want containing %q",
					err,
					test.wantError,
				)
			}
		})
	}
}

func TestAdaptEvaluationRejectsMissingExpectedCase(t *testing.T) {
	runtime := &evaluationRuntime{}
	_, err := runtime.adaptEvaluation(
		&evaluation.EvaluationResult{
			EvalSetID: "validation",
			EvalCases: []*evaluation.EvaluationCaseResult{
				successfulEvaluationCaseResult("healthy"),
			},
		},
		&evalset.EvalSet{
			EvalSetID: "validation",
			EvalCases: []*evalset.EvalCase{
				{EvalID: "healthy"},
				{EvalID: "missing"},
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("adaptEvaluation error = %v, want missing-case error", err)
	}
}

func successfulEvaluationCaseResult(
	caseID string,
) *evaluation.EvaluationCaseResult {
	result := evaluationCaseResult(
		caseID,
		status.EvalStatusPassed,
		"",
		"",
	)
	result.EvalCaseResults[0].FinalEvalStatus = status.EvalStatusPassed
	result.EvalCaseResults[0].OverallEvalMetricResults = []*evalresult.EvalMetricResult{{
		MetricName: "quality",
		Score:      1,
		Threshold:  0.5,
		EvalStatus: status.EvalStatusPassed,
	}}
	return result
}

func evaluationCaseResult(
	caseID string,
	inferenceStatus status.EvalStatus,
	inferenceError string,
	evaluationError string,
) *evaluation.EvaluationCaseResult {
	return &evaluation.EvaluationCaseResult{
		EvalCaseID: caseID,
		EvalCaseResults: []*evalresult.EvalCaseResult{{
			EvalID:          caseID,
			FinalEvalStatus: status.EvalStatusFailed,
			ErrorMessage:    evaluationError,
		}},
		RunDetails: []*evaluation.EvaluationCaseRunDetails{{
			RunID: 1,
			Inference: &evaluation.EvaluationInferenceDetails{
				Status:       inferenceStatus,
				ErrorMessage: inferenceError,
			},
		}},
	}
}
