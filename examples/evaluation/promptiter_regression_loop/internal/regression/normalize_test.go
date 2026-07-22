// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestNormalizeAgentEvaluationUsesRubricReasonFallback(t *testing.T) {
	result, err := NormalizeAgentEvaluation(&evaluation.EvaluationResult{
		EvalSetID:     "validation",
		OverallStatus: status.EvalStatusFailed,
		EvalCases: []*evaluation.EvaluationCaseResult{{
			EvalCaseID:    "structured-case",
			OverallStatus: status.EvalStatusFailed,
			MetricResults: []*evalresult.EvalMetricResult{{
				MetricName: "rubric_quality",
				Score:      0,
				Threshold:  1,
				EvalStatus: status.EvalStatusFailed,
				Details: &evalresult.EvalMetricResultDetails{
					RubricScores: []*evalresult.RubricScore{{
						ID:     "structured-output",
						Score:  0,
						Reason: "rubric found a structured output schema mismatch",
					}},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("NormalizeAgentEvaluation() error = %v", err)
	}
	if got := result.Cases[0].Metrics[0].Reason; got != "rubric found a structured output schema mismatch" {
		t.Fatalf("normalized reason = %q, want rubric reason", got)
	}
	attribution := AttributeFailures(result, AttributionCatalog{MetricKinds: map[string]MetricKind{
		"rubric_quality": MetricUnknown,
	}})
	if len(attribution.Items) != 1 || attribution.Items[0].Category != FailureFormat {
		t.Fatalf("attribution = %+v, want structured-output format failure", attribution.Items)
	}
}
