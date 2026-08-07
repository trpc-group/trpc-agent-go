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
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
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
						Usage:  &model.Usage{},
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
		MaxValidationModelCalls: intPointer(1),
		MaxValidationToolCalls:  intPointer(1),
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

func TestNormalizeAgentEvaluationRejectsCallBudgetWithoutFinalResponse(t *testing.T) {
	result, err := NormalizeAgentEvaluation(&evaluation.EvaluationResult{
		EvalSetID:     "validation",
		OverallStatus: status.EvalStatusPassed,
		EvalCases: []*evaluation.EvaluationCaseResult{{
			EvalCaseID:    "empty-invocation",
			OverallStatus: status.EvalStatusPassed,
			MetricResults: []*evalresult.EvalMetricResult{{
				MetricName: "quality", Score: 1, Threshold: 1,
				EvalStatus: status.EvalStatusPassed,
			}},
			RunDetails: []*evaluation.EvaluationCaseRunDetails{{
				Inference: &evaluation.EvaluationInferenceDetails{
					Inferences: []*evalset.Invocation{{}},
					ExecutionTraces: []*atrace.Trace{{
						Status: atrace.TraceStatusCompleted,
						Usage:  &model.Usage{},
					}},
				},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("NormalizeAgentEvaluation() error = %v", err)
	}
	if result.Usage.Measured {
		t.Fatalf("usage = %+v, want unmeasured without a final response", result.Usage)
	}
	baseline := testEvaluation("validation", testCaseSpec{id: "empty-invocation", score: 0, passed: false})
	decision, err := Decide(GatePolicy{
		MinValidationScoreGain:  1,
		MaxValidationModelCalls: intPointer(1),
	}, GateInput{OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: result})
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Accepted || !reasonsContain(decision.Reasons, "usage is not measured") {
		t.Fatalf("decision = %+v, want unmeasured-usage rejection", decision)
	}
}

func TestNormalizeAgentEvaluationRejectsTokenBudgetWithoutCompleteLLMUsage(t *testing.T) {
	tests := []struct {
		name  string
		steps []atrace.Step
	}{
		{name: "all llm usage missing", steps: []atrace.Step{{NodeType: "llm"}}},
		{name: "all agent usage missing", steps: []atrace.Step{{NodeType: "agent"}}},
		{name: "partially missing", steps: []atrace.Step{
			{NodeType: "llm", Usage: &model.Usage{TotalTokens: 1}},
			{NodeType: "llm"},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := NormalizeAgentEvaluation(&evaluation.EvaluationResult{
				EvalSetID:     "validation",
				OverallStatus: status.EvalStatusPassed,
				EvalCases: []*evaluation.EvaluationCaseResult{{
					EvalCaseID:    "missing-token-usage",
					OverallStatus: status.EvalStatusPassed,
					MetricResults: []*evalresult.EvalMetricResult{{
						MetricName: "quality", Score: 1, Threshold: 1,
						EvalStatus: status.EvalStatusPassed,
					}},
					RunDetails: []*evaluation.EvaluationCaseRunDetails{{
						Inference: &evaluation.EvaluationInferenceDetails{
							Inferences: []*evalset.Invocation{{
								FinalResponse: &model.Message{Role: model.RoleAssistant},
							}},
							ExecutionTraces: []*atrace.Trace{{
								Status: atrace.TraceStatusCompleted,
								Steps:  test.steps,
							}},
						},
					}},
				}},
			})
			if err != nil {
				t.Fatalf("NormalizeAgentEvaluation() error = %v", err)
			}
			if result.Usage.Measured {
				t.Fatalf("usage = %+v, want unmeasured without complete LLM token usage", result.Usage)
			}
			baseline := testEvaluation("validation", testCaseSpec{id: "missing-token-usage", score: 0, passed: false})
			decision, err := Decide(GatePolicy{
				MinValidationScoreGain: 1,
				MaxValidationTokens:    intPointer(100),
			}, GateInput{OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: result})
			if err != nil {
				t.Fatalf("Decide() error = %v", err)
			}
			if decision.Accepted || !reasonsContain(decision.Reasons, "usage is not measured") {
				t.Fatalf("decision = %+v, want unmeasured-usage rejection", decision)
			}
		})
	}
}

func TestTraceUsageSupportsAgentNodeType(t *testing.T) {
	usage := traceUsage(&atrace.Trace{Steps: []atrace.Step{{
		NodeType: "agent",
		Usage:    &model.Usage{TotalTokens: 7},
	}}})
	if !usage.Measured || usage.ModelCalls != 1 || usage.TotalTokens != 7 {
		t.Fatalf("trace usage = %+v, want one measured model call with 7 tokens", usage)
	}
}

func TestNormalizeAgentEvaluationAggregatesEveryInvocationTrace(t *testing.T) {
	tests := []struct {
		name         string
		secondTrace  *atrace.Trace
		wantTokens   int
		wantMeasured bool
		wantReason   string
	}{
		{
			name: "counts second trace tokens",
			secondTrace: &atrace.Trace{
				Status: atrace.TraceStatusCompleted,
				Usage:  &model.Usage{TotalTokens: 60},
			},
			wantTokens: 120, wantMeasured: true,
			wantReason: "validation tokens 120 exceed budget 100",
		},
		{
			name: "marks missing second usage unmeasured",
			secondTrace: &atrace.Trace{
				Status: atrace.TraceStatusCompleted,
			},
			wantTokens: 60, wantMeasured: false,
			wantReason: "usage is not measured",
		},
		{
			name:        "marks missing second trace unmeasured",
			secondTrace: nil,
			wantTokens:  60, wantMeasured: false,
			wantReason: "usage is not measured",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := NormalizeAgentEvaluation(&evaluation.EvaluationResult{
				EvalSetID:     "validation",
				OverallStatus: status.EvalStatusPassed,
				EvalCases: []*evaluation.EvaluationCaseResult{{
					EvalCaseID:    "multi-turn",
					OverallStatus: status.EvalStatusPassed,
					MetricResults: []*evalresult.EvalMetricResult{{
						MetricName: "quality", Score: 1, Threshold: 1,
						EvalStatus: status.EvalStatusPassed,
					}},
					RunDetails: []*evaluation.EvaluationCaseRunDetails{{
						Inference: &evaluation.EvaluationInferenceDetails{
							Inferences: []*evalset.Invocation{
								{FinalResponse: &model.Message{Role: model.RoleAssistant}},
								{FinalResponse: &model.Message{Role: model.RoleAssistant}},
							},
							ExecutionTraces: []*atrace.Trace{
								{
									Status: atrace.TraceStatusCompleted,
									Usage:  &model.Usage{TotalTokens: 60},
								},
								test.secondTrace,
							},
						},
					}},
				}},
			})
			if err != nil {
				t.Fatalf("NormalizeAgentEvaluation() error = %v", err)
			}
			if got := result.Usage.TotalTokens; got != test.wantTokens {
				t.Fatalf("usage tokens = %d, want %d", got, test.wantTokens)
			}
			if got := result.Usage.Measured; got != test.wantMeasured {
				t.Fatalf("usage measured = %t, want %t", got, test.wantMeasured)
			}
			baseline := testEvaluation("validation", testCaseSpec{id: "multi-turn", score: 0, passed: false})
			decision, err := Decide(GatePolicy{
				MinValidationScoreGain: 1,
				MaxValidationTokens:    intPointer(100),
			}, GateInput{OriginalBaseline: baseline, AcceptedBaseline: baseline, Candidate: result})
			if err != nil {
				t.Fatalf("Decide() error = %v", err)
			}
			if decision.Accepted || !reasonsContain(decision.Reasons, test.wantReason) {
				t.Fatalf("decision = %+v, want reason containing %q", decision, test.wantReason)
			}
		})
	}
}

func TestNormalizeEngineEvaluationUsesStableStatusSeverityAndScores(t *testing.T) {
	metrics := []promptiterengine.MetricResult{
		{MetricName: "passed", Score: 1, Status: status.EvalStatusPassed},
		{MetricName: "skipped", Score: 100, Status: status.EvalStatusNotEvaluated},
		{MetricName: "unknown", Score: 0.5, Status: status.EvalStatusUnknown},
		{MetricName: "failed", Score: 0, Status: status.EvalStatusFailed},
	}
	orders := [][]promptiterengine.MetricResult{
		metrics,
		{metrics[3], metrics[2], metrics[1], metrics[0]},
	}
	for index, ordered := range orders {
		result, err := NormalizeEngineEvaluation(&promptiterengine.EvaluationResult{
			OverallScore: 0.5,
			EvalSets: []promptiterengine.EvalSetResult{{
				EvalSetID: "validation",
				Cases: []promptiterengine.CaseResult{{
					EvalCaseID: "mixed-status", Metrics: ordered,
				}},
			}},
		})
		if err != nil {
			t.Fatalf("NormalizeEngineEvaluation(order %d) error = %v", index, err)
		}
		if result.OverallStatus != status.EvalStatusFailed {
			t.Fatalf("order %d status = %q, want failed", index, result.OverallStatus)
		}
		if got, want := result.Cases[0].Score, 0.5; got != want {
			t.Fatalf("order %d case score = %v, want %v", index, got, want)
		}
	}
}

func TestNormalizeEngineEvaluationRejectsInvalidIdentities(t *testing.T) {
	validResult := func() *promptiterengine.EvaluationResult {
		return &promptiterengine.EvaluationResult{
			OverallScore: 1,
			EvalSets: []promptiterengine.EvalSetResult{{
				EvalSetID: "validation",
				Cases: []promptiterengine.CaseResult{{
					EvalCaseID: "case-1",
					Metrics: []promptiterengine.MetricResult{{
						MetricName: "quality",
						Score:      1,
						Status:     status.EvalStatusPassed,
					}},
				}},
			}},
		}
	}
	tests := []struct {
		name   string
		mutate func(*promptiterengine.EvaluationResult)
	}{
		{
			name: "empty eval set id",
			mutate: func(result *promptiterengine.EvaluationResult) {
				result.EvalSets[0].EvalSetID = ""
			},
		},
		{
			name: "empty case id",
			mutate: func(result *promptiterengine.EvaluationResult) {
				result.EvalSets[0].Cases[0].EvalCaseID = ""
			},
		},
		{
			name: "empty metric name",
			mutate: func(result *promptiterengine.EvaluationResult) {
				result.EvalSets[0].Cases[0].Metrics[0].MetricName = ""
			},
		},
		{
			name: "duplicate case",
			mutate: func(result *promptiterengine.EvaluationResult) {
				result.EvalSets[0].Cases = append(
					result.EvalSets[0].Cases,
					result.EvalSets[0].Cases[0],
				)
			},
		},
		{
			name: "duplicate metric",
			mutate: func(result *promptiterengine.EvaluationResult) {
				result.EvalSets[0].Cases[0].Metrics = append(
					result.EvalSets[0].Cases[0].Metrics,
					result.EvalSets[0].Cases[0].Metrics[0],
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := validResult()
			test.mutate(result)
			if _, err := NormalizeEngineEvaluation(result); err == nil {
				t.Fatal("NormalizeEngineEvaluation() error = nil, want identity error")
			}
		})
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
					Inferences: []*evalset.Invocation{{
						FinalResponse: &model.Message{Role: model.RoleAssistant},
					}},
					ExecutionTraces: []*atrace.Trace{{
						Status: atrace.TraceStatusCompleted,
						Usage:  &model.Usage{TotalTokens: 1},
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
