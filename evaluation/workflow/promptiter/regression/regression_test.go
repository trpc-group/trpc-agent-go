//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"math"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestSummarize(t *testing.T) {
	result := &evaluation.EvaluationResult{
		AppName: "app", EvalSetID: "set", ExecutionTime: 1500 * time.Millisecond,
		EvalCases: []*evaluation.EvaluationCaseResult{
			evalCase("b", metric("quality", 0.5, status.EvalStatusFailed, finalCriterion())),
			evalCase("a",
				metric("quality", 1, status.EvalStatusPassed, finalCriterion()),
				metric("unused", 0, status.EvalStatusNotEvaluated, nil),
			),
		},
	}
	result.EvalCases[0].EvalCaseResults = []*evalresult.EvalCaseResult{{
		RunID: 1,
		EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{{
			ActualInvocation: &evalset.Invocation{
				FinalResponse:  &model.Message{Content: "actual"},
				Tools:          []*evalset.Tool{{Name: "search", Arguments: map[string]any{"q": "go"}}},
				ExecutionTrace: &trace.Trace{Steps: []trace.Step{{AgentName: "router", Branch: "answer", NodeID: "n1"}}},
			},
			ExpectedInvocation: &evalset.Invocation{FinalResponse: &model.Message{Content: "expected"}},
		}},
	}}

	summary, err := Summarize(result)
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if summary.Score != 0.75 || summary.Passed || summary.LatencyMS != 1500 {
		t.Fatalf("summary = %+v", summary)
	}
	if got := []string{summary.Cases[0].ID, summary.Cases[1].ID}; got[0] != "a" || got[1] != "b" {
		t.Fatalf("case order = %v", got)
	}
	evidence := summary.Cases[1].ActualInvocations[0]
	if evidence.FinalResponse != "actual" || evidence.Tools[0].Name != "search" || evidence.Route[0].Agent != "router" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestSummarizeMetricEvidence(t *testing.T) {
	judge := metric("judge", 0, status.EvalStatusFailed, &criterion.Criterion{LLMJudge: &llm.LLMCriterion{
		Rubrics: []*llm.Rubric{{Type: "knowledge"}, {Type: "format"}},
	}})
	toolMetric := metric("tools", 1, status.EvalStatusPassed, &criterion.Criterion{
		ToolTrajectory: &tooltrajectory.ToolTrajectoryCriterion{OrderSensitive: true},
	})
	caseResult := evalCase("case", judge, toolMetric)
	caseResult.EvalCaseResults = []*evalresult.EvalCaseResult{{
		EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{{
			EvalMetricResults: []*evalresult.EvalMetricResult{{
				MetricName: "judge", Details: &evalresult.EvalMetricResultDetails{Reason: "missing citation"},
			}},
		}},
	}}
	summary, err := Summarize(&evaluation.EvaluationResult{EvalSetID: "set", EvalCases: []*evaluation.EvaluationCaseResult{caseResult}})
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	metrics := summary.Cases[0].Metrics
	if metrics[0].Reason != "missing citation" || strings.Join(metrics[0].RubricTypes, ",") != "format,knowledge" {
		t.Fatalf("judge metric = %+v", metrics[0])
	}
	if metrics[1].Criterion != "tool_trajectory" || !metrics[1].ToolOrderSensitive {
		t.Fatalf("tool metric = %+v", metrics[1])
	}
}

func TestSummarizeUsesRubricReasons(t *testing.T) {
	detailed := metric("details", 0, status.EvalStatusFailed, nil)
	detailed.Details = &evalresult.EvalMetricResultDetails{RubricScores: []*evalresult.RubricScore{{Reason: "details rubric"}}}
	perInvocation := metric("invocation", 0, status.EvalStatusFailed, nil)
	caseResult := evalCase("case", detailed, perInvocation)
	caseResult.EvalCaseResults = []*evalresult.EvalCaseResult{{
		EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{{
			EvalMetricResults: []*evalresult.EvalMetricResult{{
				MetricName: "invocation", Details: &evalresult.EvalMetricResultDetails{
					RubricScores: []*evalresult.RubricScore{{Reason: "invocation rubric"}},
				},
			}},
		}},
	}}
	summary, err := Summarize(&evaluation.EvaluationResult{EvalSetID: "set", EvalCases: []*evaluation.EvaluationCaseResult{caseResult}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Cases[0].Metrics[0].Reason != "details rubric" || summary.Cases[0].Metrics[1].Reason != "invocation rubric" {
		t.Fatalf("metric reasons = %+v", summary.Cases[0].Metrics)
	}
}

func TestSummarizeKeepsFailedRunTraceAndStructuredContent(t *testing.T) {
	textPart := "structured answer"
	started := time.Unix(100, 0)
	runTrace := &trace.Trace{
		RootInvocationID: "invocation", Status: trace.TraceStatusIncomplete,
		StartedAt: started, EndedAt: started.Add(250 * time.Millisecond),
		Usage: &model.Usage{TotalTokens: 17},
		Steps: []trace.Step{{AgentName: "worker", NodeID: "tool", Error: "tool failed"}},
	}
	failedCase := evalCase("failed", metric("quality", 0, status.EvalStatusFailed, finalCriterion()))
	failedCase.EvalCaseResults = []*evalresult.EvalCaseResult{{
		RunID: 3, ErrorMessage: "runner stopped",
		EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{{
			ActualInvocation: &evalset.Invocation{
				InvocationID: "invocation",
				FinalResponse: &model.Message{ContentParts: []model.ContentPart{
					{Type: model.ContentTypeText, Text: &textPart},
					{Type: model.ContentTypeImage},
				}},
				Tools: []*evalset.Tool{{Name: "lookup", Arguments: map[string]any{
					"openai_api_key": "leak", "token": "leak", "database-password": "leak",
					"nested": map[string]any{"accessToken": "leak", "clientSecret": "leak"},
				}, Result: map[string]any{"secret": "leak", "clientSecret": "leak"}}},
			},
		}},
	}}
	failedCase.RunDetails = []*evaluation.EvaluationCaseRunDetails{{
		RunID: 3,
		Inference: &evaluation.EvaluationInferenceDetails{
			Status: status.EvalStatusFailed, ErrorMessage: "runner stopped",
			ExecutionTraces: []*trace.Trace{runTrace},
		},
	}}

	summary, err := Summarize(&evaluation.EvaluationResult{
		EvalSetID: "set", EvalCases: []*evaluation.EvaluationCaseResult{failedCase},
	})
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	actual := summary.Cases[0].ActualInvocations[0]
	if actual.FinalResponse != "structured answer\n[image]" || actual.Route[0].Error != "tool failed" {
		t.Fatalf("failed evidence = %+v", actual)
	}
	if strings.Contains(string(actual.Tools[0].Arguments), "leak") || strings.Contains(string(actual.Tools[0].Result), "leak") {
		t.Fatalf("tool evidence leaked: arguments=%s result=%s", actual.Tools[0].Arguments, actual.Tools[0].Result)
	}
	if summary.Cost.ModelCalls != 0 || summary.Cost.Tokens != 17 || summary.Cost.LatencyMS != 250 {
		t.Fatalf("cost = %+v", summary.Cost)
	}
	if summary.Cases[0].Passed {
		t.Fatal("failed run was marked passed")
	}
}

func TestSummarizePreservesExecutionFailureWithoutMetrics(t *testing.T) {
	failedCase := &evaluation.EvaluationCaseResult{
		EvalCaseID:    "failed",
		OverallStatus: status.EvalStatusFailed,
		EvalCaseResults: []*evalresult.EvalCaseResult{{
			RunID: 1, ErrorMessage: "inference unavailable",
		}},
	}
	summary, err := summarize(&evaluation.EvaluationResult{
		EvalSetID: "set", EvalCases: []*evaluation.EvaluationCaseResult{failedCase},
	}, []string{"quality"})
	if err != nil {
		t.Fatalf("Summarize() error = %v", err)
	}
	if summary.Score != 0 || summary.Passed || len(summary.Cases[0].Metrics) != 1 ||
		summary.Cases[0].Error != "inference unavailable" {
		t.Fatalf("summary = %+v", summary)
	}
	attribution, err := Attribute(summary)
	if err != nil {
		t.Fatalf("Attribute() error = %v", err)
	}
	if len(attribution.Failures) != 1 || attribution.Failures[0].Category != FailureExecutionError {
		t.Fatalf("attribution = %+v", attribution)
	}
}

func TestSafeJSONHashesLongToolResult(t *testing.T) {
	result := safeJSON(map[string]any{"result": strings.Repeat("x", 20<<10), "bearerToken": "leak"})
	if strings.Contains(string(result), "leak") || !strings.Contains(string(result), `"truncated":true`) ||
		!strings.Contains(string(result), `"sha256"`) {
		t.Fatalf("safeJSON() = %s", result)
	}
}

func TestSafeJSONRejectsUnencodableValuesAndRedactsArrays(t *testing.T) {
	if got := string(safeJSON(make(chan int))); got != "null" {
		t.Fatalf("safeJSON(channel) = %s", got)
	}
	got := string(safeJSON([]any{map[string]any{"authorization": "leak"}, "safe"}))
	if strings.Contains(got, "leak") || !strings.Contains(got, "redacted") {
		t.Fatalf("safeJSON(array) = %s", got)
	}
}

func TestSummarizeRejectsInvalidResults(t *testing.T) {
	tests := []struct {
		name   string
		result *evaluation.EvaluationResult
	}{
		{name: "nil"},
		{name: "empty eval set", result: &evaluation.EvaluationResult{}},
		{name: "no metrics", result: &evaluation.EvaluationResult{EvalSetID: "set", EvalCases: []*evaluation.EvaluationCaseResult{{EvalCaseID: "case"}}}},
		{name: "duplicate cases", result: &evaluation.EvaluationResult{EvalSetID: "set", EvalCases: []*evaluation.EvaluationCaseResult{
			evalCase("case", metric("m", 1, status.EvalStatusPassed, nil)),
			evalCase("case", metric("m", 1, status.EvalStatusPassed, nil)),
		}}},
		{name: "non finite", result: &evaluation.EvaluationResult{EvalSetID: "set", EvalCases: []*evaluation.EvaluationCaseResult{
			evalCase("case", metric("m", math.NaN(), status.EvalStatusPassed, nil)),
		}}},
		{name: "duplicate metrics", result: &evaluation.EvaluationResult{EvalSetID: "set", EvalCases: []*evaluation.EvaluationCaseResult{
			evalCase("case", metric("m", 1, status.EvalStatusPassed, nil), metric("m", 1, status.EvalStatusPassed, nil)),
		}}},
		{name: "no evaluated metrics", result: &evaluation.EvaluationResult{EvalSetID: "set", EvalCases: []*evaluation.EvaluationCaseResult{
			evalCase("case", metric("m", 0, status.EvalStatusNotEvaluated, nil)),
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Summarize(test.result); err == nil {
				t.Fatal("Summarize() error = nil")
			}
		})
	}
}

func TestSummarizeFallsBackToInvocationTrace(t *testing.T) {
	started := time.Unix(10, 0)
	result := pipelineResult("set", 0, false)
	result.EvalCases[0].EvalCaseResults = []*evalresult.EvalCaseResult{{
		RunID: 1, EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{{
			ActualInvocation: &evalset.Invocation{InvocationID: "inv", ExecutionTrace: &trace.Trace{
				StartedAt: started, EndedAt: started.Add(20 * time.Millisecond), Usage: &model.Usage{TotalTokens: 7},
				Status: trace.TraceStatusIncomplete,
			}},
		}},
	}}
	summary, err := Summarize(result)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Cost.Tokens != 7 || summary.Cost.LatencyMS != 20 || summary.Cases[0].Passed {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestSummarizeAlignsRunDetailsTraceByInvocationID(t *testing.T) {
	result := pipelineResult("set", 0, false)
	result.EvalCases[0].EvalCaseResults = []*evalresult.EvalCaseResult{{
		RunID: 7, EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{{
			ActualInvocation: &evalset.Invocation{InvocationID: "wanted"},
		}},
	}}
	result.EvalCases[0].RunDetails = []*evaluation.EvaluationCaseRunDetails{{
		RunID: 7, Inference: &evaluation.EvaluationInferenceDetails{ExecutionTraces: []*trace.Trace{
			{RootInvocationID: "other", Steps: []trace.Step{{AgentName: "wrong"}}},
			{RootInvocationID: "wanted", Steps: []trace.Step{{AgentName: "matched"}}},
		}},
	}}
	summary, err := Summarize(result)
	if err != nil {
		t.Fatal(err)
	}
	route := summary.Cases[0].ActualInvocations[0].Route
	if len(route) != 1 || route[0].Agent != "matched" {
		t.Fatalf("aligned route = %+v", route)
	}
}

func TestTraceExecutionFailed(t *testing.T) {
	tests := []*trace.Trace{
		{Status: trace.TraceStatusFailed},
		{Status: trace.TraceStatusIncomplete},
		{Status: trace.TraceStatusCompleted, Steps: []trace.Step{{Error: "tool failed"}}},
	}
	for _, executionTrace := range tests {
		if !traceExecutionFailed(executionTrace) {
			t.Fatalf("traceExecutionFailed(%+v) = false", executionTrace)
		}
	}
	if traceExecutionFailed(&trace.Trace{Status: trace.TraceStatusCompleted}) {
		t.Fatal("completed trace was marked failed")
	}
}

func TestCompareClassifications(t *testing.T) {
	tests := []struct {
		name                      string
		before, after             float64
		beforePassed, afterPassed bool
		want                      DeltaKind
	}{
		{name: "newly passed", before: 0, after: 1, afterPassed: true, want: DeltaNewlyPassed},
		{name: "newly failed", before: 1, after: 0, beforePassed: true, want: DeltaNewlyFailed},
		{name: "improved", before: 0.4, after: 0.6, want: DeltaImproved},
		{name: "regressed", before: 0.6, after: 0.4, want: DeltaRegressed},
		{name: "unchanged", before: 0.5, after: 0.5 + scoreDeltaEpsilon/2, want: DeltaUnchanged},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, _, err := classifyDelta(test.before, test.after, test.beforePassed, test.afterPassed)
			if err != nil || kind != test.want {
				t.Fatalf("classifyDelta() = %q, %v; want %q", kind, err, test.want)
			}
		})
	}
}

func TestCompareSortsAndComparesMetrics(t *testing.T) {
	baseline := summary("set", 0.5, false, CaseSummary{
		ID: "case", Score: 0.5, Metrics: []MetricSummary{
			{Name: "z", Score: 0, Evaluated: false},
			{Name: "a", Score: 0.5, Evaluated: true},
		},
	})
	candidate := summary("set", 1, true, CaseSummary{
		ID: "case", Score: 1, Passed: true, Metrics: []MetricSummary{
			{Name: "a", Score: 1, Passed: true, Evaluated: true},
			{Name: "z", Score: 0, Evaluated: false},
		},
	})
	delta, err := Compare(baseline, candidate)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if delta.Kind != DeltaNewlyPassed || delta.Cases[0].Metrics[0].Name != "a" || delta.Cases[0].Metrics[1].Kind != DeltaUnchanged {
		t.Fatalf("delta = %+v", delta)
	}
	if !delta.Cases[0].Metrics[0].CandidatePassed || delta.Cases[0].Metrics[0].BaselinePassed {
		t.Fatalf("metric pass states = %+v", delta.Cases[0].Metrics[0])
	}
}

func TestCompareRejectsDifferentShapes(t *testing.T) {
	baseline := summary("set", 1, true, CaseSummary{ID: "a"})
	tests := []*EvalSummary{
		summary("other", 1, true, CaseSummary{ID: "a"}),
		summary("set", 1, true, CaseSummary{ID: "b"}),
		summary("set", 1, true, CaseSummary{ID: "a", Metrics: []MetricSummary{{Name: "new"}}}),
	}
	for _, candidate := range tests {
		if _, err := Compare(baseline, candidate); err == nil {
			t.Fatalf("Compare(%+v) error = nil", candidate)
		}
	}
}

func TestCompareRejectsInvalidCaseAndMetricIdentities(t *testing.T) {
	tests := [][2]*EvalSummary{
		{summary("set", 1, true, CaseSummary{}), summary("set", 1, true, CaseSummary{ID: "case"})},
		{summary("set", 1, true, CaseSummary{ID: "case"}, CaseSummary{ID: "case"}), summary("set", 1, true, CaseSummary{ID: "case"})},
		{summary("set", 1, true, CaseSummary{ID: "case", Metrics: []MetricSummary{{}}}), summary("set", 1, true, CaseSummary{ID: "case", Metrics: []MetricSummary{{Name: "m"}}})},
		{summary("set", 1, true, CaseSummary{ID: "case", Metrics: []MetricSummary{{Name: "m"}, {Name: "m"}}}), summary("set", 1, true, CaseSummary{ID: "case", Metrics: []MetricSummary{{Name: "m"}}})},
	}
	for _, pair := range tests {
		if _, err := Compare(pair[0], pair[1]); err == nil {
			t.Fatalf("Compare(%+v, %+v) error = nil", pair[0], pair[1])
		}
	}
}

func evalCase(id string, metrics ...*evalresult.EvalMetricResult) *evaluation.EvaluationCaseResult {
	return &evaluation.EvaluationCaseResult{EvalCaseID: id, MetricResults: metrics}
}

func metric(name string, score float64, evalStatus status.EvalStatus, c *criterion.Criterion) *evalresult.EvalMetricResult {
	return &evalresult.EvalMetricResult{MetricName: name, Score: score, Threshold: 0.5, EvalStatus: evalStatus, Criterion: c}
}

func finalCriterion() *criterion.Criterion {
	return &criterion.Criterion{FinalResponse: &finalresponse.FinalResponseCriterion{}}
}

func summary(id string, score float64, passed bool, cases ...CaseSummary) *EvalSummary {
	return &EvalSummary{EvalSetID: id, Score: score, Passed: passed, Cases: cases}
}
