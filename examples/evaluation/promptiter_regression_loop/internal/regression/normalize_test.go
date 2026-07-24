// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"testing"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
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

func TestNormalizeAgentEvaluationCountsInvocationUsage(t *testing.T) {
	result, err := NormalizeAgentEvaluation(&evaluation.EvaluationResult{
		EvalSetID:     "validation",
		OverallStatus: status.EvalStatusPassed,
		EvalCases: []*evaluation.EvaluationCaseResult{{
			EvalCaseID:    "tool-case",
			OverallStatus: status.EvalStatusPassed,
			MetricResults: []*evalresult.EvalMetricResult{{
				MetricName: "quality",
				Score:      1,
				Threshold:  1,
				EvalStatus: status.EvalStatusPassed,
			}},
			RunDetails: []*evaluation.EvaluationCaseRunDetails{{
				Inference: &evaluation.EvaluationInferenceDetails{
					Inferences: []*evalset.Invocation{{
						IntermediateResponses: []*model.Message{{Role: model.RoleAssistant}},
						FinalResponse:         &model.Message{Role: model.RoleAssistant},
						Tools: []*evalset.Tool{
							{ID: "call-1"},
							{ID: "call-2"},
						},
					}},
					ExecutionTraces: []*atrace.Trace{{
						Status: atrace.TraceStatusCompleted,
						Steps:  []atrace.Step{{NodeType: "llm"}},
					}},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("NormalizeAgentEvaluation() error = %v", err)
	}
	usage := result.Cases[0].Trace.Usage
	if usage.ModelCalls != 2 || usage.ToolCalls != 2 {
		t.Fatalf("usage = %+v, want 2 model calls and 2 tool calls", usage)
	}
	baseline := testEvaluation("validation", testCaseSpec{id: "tool-case", score: 0, passed: false})
	decision, err := Decide(GatePolicy{
		MinValidationScoreGain:  1,
		MaxValidationModelCalls: 1,
		MaxValidationToolCalls:  1,
	}, GateInput{
		OriginalBaseline: baseline,
		AcceptedBaseline: baseline,
		Candidate:        result,
	})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Accepted ||
		!reasonsContain(decision.Reasons, "model calls 2 exceed budget 1") ||
		!reasonsContain(decision.Reasons, "tool calls 2 exceed budget 1") {
		t.Fatalf("decision = %+v, want model and tool budget rejection", decision)
	}
}

func TestNormalizeAgentEvaluationMarksMissingInvocationUsageUnmeasured(t *testing.T) {
	result, err := NormalizeAgentEvaluation(&evaluation.EvaluationResult{
		EvalSetID:     "validation",
		OverallStatus: status.EvalStatusPassed,
		EvalCases: []*evaluation.EvaluationCaseResult{{
			EvalCaseID:    "missing-details",
			OverallStatus: status.EvalStatusPassed,
			MetricResults: []*evalresult.EvalMetricResult{{
				MetricName: "quality",
				Score:      1,
				Threshold:  1,
				EvalStatus: status.EvalStatusPassed,
			}},
			RunDetails: []*evaluation.EvaluationCaseRunDetails{{
				Inference: &evaluation.EvaluationInferenceDetails{
					ExecutionTraces: []*atrace.Trace{{
						Status: atrace.TraceStatusCompleted,
						Steps:  []atrace.Step{{NodeType: "llm"}},
					}},
				},
			}, nil},
		}},
	})
	if err != nil {
		t.Fatalf("NormalizeAgentEvaluation() error = %v", err)
	}
	if result.Usage.Measured {
		t.Fatalf("usage = %+v, want unmeasured without invocation details", result.Usage)
	}
}
