//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression_test

import (
	"testing"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

type releaseScenario struct {
	name      string
	expected  regression.Decision
	configure func(*regression.RunSpec, *engine.RunResult, *regression.UsageSummary)
}

func TestReleaseDecisionAccuracyUsesCompletePipelineScenarios(t *testing.T) {
	scenarios := []releaseScenario{
		{name: "generalized candidate improves validation", expected: regression.DecisionAccepted},
		{
			name:     "PromptIter exploration rejection does not replace release evidence",
			expected: regression.DecisionAccepted,
			configure: func(_ *regression.RunSpec, source *engine.RunResult, _ *regression.UsageSummary) {
				source.Rounds[0].Acceptance.Accepted = false
				source.Rounds[0].Acceptance.Reason = "exploration threshold not met"
				source.AcceptedProfile = source.InitialProfile
			},
		},
		{
			name:     "candidate changes behavior but does not improve validation",
			expected: regression.DecisionRejected,
			configure: func(_ *regression.RunSpec, source *engine.RunResult, _ *regression.UsageSummary) {
				setEvaluationMetric(source.Rounds[0].Validation, "quality", 0, status.EvalStatusFailed, "no validation gain")
			},
		},
		{
			name:     "training improves much more than validation",
			expected: regression.DecisionRejected,
			configure: func(spec *regression.RunSpec, source *engine.RunResult, _ *regression.UsageSummary) {
				spec.Gate.MaxGeneralizationGap = .2
				setEvaluationMetric(source.BaselineValidation, "quality", .8, status.EvalStatusPassed, "")
				appendFollowUpRound(
					source, profile("follow-up"),
					evaluationResult("train", "train-case", 1, status.EvalStatusPassed, ""),
					evaluationResult("validation", "validation-case", .8, status.EvalStatusPassed, ""),
					false,
				)
			},
		},
		{
			name:     "candidate introduces a safety hard failure",
			expected: regression.DecisionRejected,
			configure: func(spec *regression.RunSpec, source *engine.RunResult, _ *regression.UsageSummary) {
				spec.MetricPolicies["safety"] = regression.MetricPolicy{Weight: 3, Floor: 1, HardFail: true}
				appendEvaluationMetric(source.BaselineValidation, "safety", 1, status.EvalStatusPassed, "")
				appendEvaluationMetric(source.Rounds[0].Validation, "safety", 0, status.EvalStatusFailed, "private data disclosed")
			},
		},
		{
			name:     "final candidate has no later training evidence",
			expected: regression.DecisionInconclusive,
			configure: func(spec *regression.RunSpec, _ *engine.RunResult, _ *regression.UsageSummary) {
				spec.Gate.MaxGeneralizationGap = .2
			},
		},
		{
			name:     "budget is configured but only evaluation usage is known",
			expected: regression.DecisionInconclusive,
			configure: func(spec *regression.RunSpec, _ *engine.RunResult, usage *regression.UsageSummary) {
				spec.Budget.MaxCalls = 10
				*usage = regression.UsageSummary{Calls: 4, Source: "evaluation_traces"}
			},
		},
		{
			name:     "evaluation emits an unconfigured metric",
			expected: regression.DecisionInconclusive,
			configure: func(_ *regression.RunSpec, source *engine.RunResult, _ *regression.UsageSummary) {
				appendEvaluationMetric(source.BaselineValidation, "new_metric", 0, status.EvalStatusFailed, "")
				appendEvaluationMetric(source.Rounds[0].Validation, "new_metric", 1, status.EvalStatusPassed, "")
			},
		},
		{
			name:     "configured metric is absent from every case",
			expected: regression.DecisionInconclusive,
			configure: func(spec *regression.RunSpec, _ *engine.RunResult, _ *regression.UsageSummary) {
				spec.MetricPolicies["safety"] = regression.MetricPolicy{
					Weight: 2, Floor: 1, HardFail: true,
				}
			},
		},
		{
			name:     "configured repeated runs are missing from evaluation evidence",
			expected: regression.DecisionInconclusive,
			configure: func(spec *regression.RunSpec, source *engine.RunResult, _ *regression.UsageSummary) {
				spec.Runtime.NumRuns = 2
				source.Configuration.EvaluationOptions.NumRuns = 2
			},
		},
		{
			name:     "one repeated run omits a configured metric",
			expected: regression.DecisionInconclusive,
			configure: func(spec *regression.RunSpec, source *engine.RunResult, _ *regression.UsageSummary) {
				spec.Runtime.NumRuns = 2
				source.Configuration.EvaluationOptions.NumRuns = 2
				appendSecondRun(source.BaselineValidation, true)
				appendSecondRun(source.Rounds[0].Train, true)
				appendSecondRun(source.Rounds[0].Validation, false)
			},
		},
		{
			name:     "candidate modifies an unrelated surface",
			expected: regression.DecisionRejected,
			configure: func(_ *regression.RunSpec, source *engine.RunResult, _ *regression.UsageSummary) {
				text := "unrelated"
				source.Rounds[0].OutputProfile.Overrides = append(
					source.Rounds[0].OutputProfile.Overrides,
					promptiter.SurfaceOverride{
						SurfaceID: "agent#global_instruction",
						Value:     astructure.SurfaceValue{Text: &text},
					},
				)
			},
		},
		{
			name:     "candidate validation omits a baseline case",
			expected: regression.DecisionInconclusive,
			configure: func(_ *regression.RunSpec, source *engine.RunResult, _ *regression.UsageSummary) {
				source.Rounds[0].Validation.EvalSets[0].Cases = nil
			},
		},
		{
			name:     "candidate validation omits a critical case",
			expected: regression.DecisionRejected,
			configure: func(spec *regression.RunSpec, source *engine.RunResult, _ *regression.UsageSummary) {
				spec.CriticalCaseIDs = []string{"validation-case"}
				source.Rounds[0].Validation.EvalSets[0].Cases = nil
			},
		},
		{
			name:     "complete usage exceeds cost budget",
			expected: regression.DecisionRejected,
			configure: func(spec *regression.RunSpec, _ *engine.RunResult, usage *regression.UsageSummary) {
				spec.Budget.MaxEstimatedCost = .5
				*usage = regression.UsageSummary{
					EstimatedCost: 1, CostKnown: true, Complete: true, Source: "full_pipeline",
				}
			},
		},
		{
			name:     "cost limit is configured but provider cost is unknown",
			expected: regression.DecisionInconclusive,
			configure: func(spec *regression.RunSpec, _ *engine.RunResult, usage *regression.UsageSummary) {
				spec.Budget.MaxEstimatedCost = .5
				*usage = regression.UsageSummary{
					Complete: true, Source: "full_pipeline",
				}
			},
		},
	}

	correct := 0
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			spec := auditSpec()
			source := promptIterResult(profile("baseline"), profile("candidate"), true)
			usage := regression.UsageSummary{Complete: true, Source: "full_pipeline"}
			if scenario.configure != nil {
				scenario.configure(spec, source, &usage)
			}
			result := analyzeWith(t, spec, source, usage)
			if result.Decision == scenario.expected {
				correct++
				return
			}
			t.Errorf("decision = %q, want %q; candidates=%+v", result.Decision, scenario.expected, result.Candidates)
		})
	}
	accuracy := float64(correct) / float64(len(scenarios))
	if accuracy < .8 {
		t.Fatalf("release scenario accuracy %.3f is below 0.80", accuracy)
	}
}

type attributionScenario struct {
	name     string
	expected regression.FailureCategory
	prepare  func(*engine.CaseResult)
}

func TestFailureAttributionAccuracyUsesEvaluationEvidenceScenarios(t *testing.T) {
	scenarios := []attributionScenario{
		{name: "final response mismatch", expected: regression.FailureFinalResponseMismatch, prepare: failedMetricScenario("task_success", "answer differs from expected")},
		{name: "tool selection mismatch", expected: regression.FailureToolSelection, prepare: failedMetricScenario("tool_selection", "expected get_order, got search_order")},
		{name: "tool argument mismatch", expected: regression.FailureToolArgument, prepare: failedMetricScenario("tool_arguments", "order_id parameter is missing")},
		{name: "route mismatch", expected: regression.FailureRoute, prepare: failedMetricScenario("route", "refund-specialist was not selected")},
		{name: "structured output mismatch", expected: regression.FailureFormat, prepare: failedMetricScenario("format", "JSON schema was not followed")},
		{name: "knowledge recall failure", expected: regression.FailureKnowledgeRecall, prepare: failedMetricScenario("knowledge_recall", "required source was not recalled")},
		{name: "safety violation", expected: regression.FailureSafetyPolicy, prepare: failedMetricScenario("safety", "private order data was disclosed")},
		{
			name: "runner fails before producing a response", expected: regression.FailureInferenceError,
			prepare: func(result *engine.CaseResult) {
				passingMetricScenario(result)
				result.RunDetails[0].Inference.Status = status.EvalStatusFailed
				result.RunDetails[0].Inference.ErrorMessage = "runner unavailable"
			},
		},
		{
			name: "structured tool backend failure", expected: regression.FailureToolResultHandling,
			prepare: func(result *engine.CaseResult) {
				passingMetricScenario(result)
				result.RunDetails[0].Inference.Inferences[0].Tools = []*evalset.Tool{{
					Name: "get_order", Result: map[string]any{"error": "backend unavailable"},
				}}
			},
		},
		{
			name: "model fails after a successful tool response", expected: regression.FailureInferenceError,
			prepare: func(result *engine.CaseResult) {
				passingMetricScenario(result)
				result.RunDetails[0].Inference.Status = status.EvalStatusFailed
				result.RunDetails[0].Inference.ErrorMessage = "model decoder unavailable"
				result.RunDetails[0].Inference.Inferences[0].Tools = []*evalset.Tool{{
					Name: "get_order", Result: map[string]any{"status": "ok"},
				}}
			},
		},
		{
			name: "execution trace step fails", expected: regression.FailureInferenceError,
			prepare: func(result *engine.CaseResult) {
				passingMetricScenario(result)
				result.RunDetails[0].Inference.ExecutionTraces[0].Steps[0].Error = "node failed"
			},
		},
		{name: "tool result is ignored", expected: regression.FailureToolResultHandling, prepare: failedMetricScenario("tool_result_quality", "tool result was ignored")},
		{name: "Chinese safety rubric", expected: regression.FailureSafetyPolicy, prepare: failedMetricScenario("llm_rubric_critic", "回答泄露了用户隐私")},
		{name: "Chinese tool argument rubric", expected: regression.FailureToolArgument, prepare: failedMetricScenario("llm_rubric_critic", "工具参数 order_id 缺失")},
		{name: "Chinese route rubric", expected: regression.FailureRoute, prepare: failedMetricScenario("llm_rubric_critic", "路由到了错误的子代理")},
		{name: "Chinese knowledge rubric", expected: regression.FailureKnowledgeRecall, prepare: failedMetricScenario("llm_rubric_critic", "未检索到所需知识和事实依据")},
		{name: "unknown custom evaluator", expected: regression.FailureUnknown, prepare: failedMetricScenario("custom_quality", "custom contract failed")},
	}

	correct := 0
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			source := promptIterResult(profile("baseline"), profile("candidate"), true)
			scenario.prepare(&source.Rounds[0].Train.EvalSets[0].Cases[0])
			result := analyzeWith(
				t, auditSpec(), source,
				regression.UsageSummary{Complete: true, Source: "full_pipeline"},
			)
			if len(result.Attributions) != 1 {
				t.Fatalf("attributions = %+v", result.Attributions)
			}
			attribution := result.Attributions[0]
			if attribution.Category == scenario.expected {
				correct++
			} else {
				t.Errorf("category = %q, want %q", attribution.Category, scenario.expected)
			}
			if attribution.Reason == "" || len(attribution.Evidence) == 0 || attribution.Evidence[0].Reason == "" {
				t.Fatalf("attribution is not explainable: %+v", attribution)
			}
		})
	}
	accuracy := float64(correct) / float64(len(scenarios))
	if accuracy < .75 {
		t.Fatalf("attribution scenario accuracy %.3f is below 0.75", accuracy)
	}
}

func failedMetricScenario(name, reason string) func(*engine.CaseResult) {
	return func(result *engine.CaseResult) {
		result.Metrics = []engine.MetricResult{{
			MetricName: name, Score: 0, Threshold: 1,
			Status: status.EvalStatusFailed, Reason: reason,
			Details: &evalresult.EvalMetricResultDetails{Reason: reason},
		}}
		result.RunResults[0].OverallEvalMetricResults = []*evalresult.EvalMetricResult{{
			MetricName: name, Score: 0, Threshold: 1,
			EvalStatus: status.EvalStatusFailed,
			Details:    &evalresult.EvalMetricResultDetails{Reason: reason},
		}}
	}
}

func passingMetricScenario(result *engine.CaseResult) {
	result.Metrics = []engine.MetricResult{{
		MetricName: "quality", Score: 1, Threshold: 1, Status: status.EvalStatusPassed,
	}}
	result.RunResults[0].OverallEvalMetricResults = []*evalresult.EvalMetricResult{{
		MetricName: "quality", Score: 1, Threshold: 1, EvalStatus: status.EvalStatusPassed,
	}}
}

func setEvaluationMetric(
	result *engine.EvaluationResult,
	name string,
	score float64,
	metricStatus status.EvalStatus,
	reason string,
) {
	caseResult := &result.EvalSets[0].Cases[0]
	caseResult.Metrics = []engine.MetricResult{{
		MetricName: name, Score: score, Threshold: 1,
		Status: metricStatus, Reason: reason,
		Details: &evalresult.EvalMetricResultDetails{Reason: reason},
	}}
	caseResult.RunResults[0].OverallEvalMetricResults = []*evalresult.EvalMetricResult{{
		MetricName: name, Score: score, Threshold: 1,
		EvalStatus: metricStatus,
		Details:    &evalresult.EvalMetricResultDetails{Reason: reason},
	}}
	result.OverallScore = score
	result.EvalSets[0].OverallScore = score
}

func appendEvaluationMetric(
	result *engine.EvaluationResult,
	name string,
	score float64,
	metricStatus status.EvalStatus,
	reason string,
) {
	caseResult := &result.EvalSets[0].Cases[0]
	caseResult.Metrics = append(caseResult.Metrics, engine.MetricResult{
		MetricName: name, Score: score, Threshold: 1,
		Status: metricStatus, Reason: reason,
		Details: &evalresult.EvalMetricResultDetails{Reason: reason},
	})
	caseResult.RunResults[0].OverallEvalMetricResults = append(
		caseResult.RunResults[0].OverallEvalMetricResults,
		&evalresult.EvalMetricResult{
			MetricName: name, Score: score, Threshold: 1,
			EvalStatus: metricStatus,
			Details:    &evalresult.EvalMetricResultDetails{Reason: reason},
		},
	)
	recomputeEvaluationScore(result)
}

func recomputeEvaluationScore(result *engine.EvaluationResult) {
	if result == nil || len(result.EvalSets) == 0 || len(result.EvalSets[0].Cases) == 0 {
		return
	}
	metrics := result.EvalSets[0].Cases[0].Metrics
	if len(metrics) == 0 {
		return
	}
	total := 0.0
	for _, metric := range metrics {
		total += metric.Score
	}
	average := total / float64(len(metrics))
	result.EvalSets[0].OverallScore = average
	result.OverallScore = average
}

func appendSecondRun(result *engine.EvaluationResult, includeMetrics bool) {
	caseResult := &result.EvalSets[0].Cases[0]
	detail := *caseResult.RunDetails[0]
	detail.RunID = 2
	caseResult.RunDetails = append(caseResult.RunDetails, &detail)
	run := *caseResult.RunResults[0]
	run.RunID = 2
	if includeMetrics {
		run.OverallEvalMetricResults = append(
			[]*evalresult.EvalMetricResult(nil), run.OverallEvalMetricResults...,
		)
	} else {
		run.OverallEvalMetricResults = nil
	}
	caseResult.RunResults = append(caseResult.RunResults, &run)
}
