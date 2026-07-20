//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestAttributeCategories(t *testing.T) {
	tool := func(name, arguments string) ToolSummary {
		return ToolSummary{Name: name, Arguments: json.RawMessage(arguments)}
	}
	invocation := func(tools []ToolSummary, route []RouteStep) []InvocationSummary {
		return []InvocationSummary{{Tools: tools, Route: route}}
	}
	tests := []struct {
		name     string
		evalCase CaseSummary
		want     FailureCategory
	}{
		{name: "execution", evalCase: failedCase("case", MetricSummary{Name: "m", Evaluated: true}, func(c *CaseSummary) {
			c.Error = "runner stopped"
		}), want: FailureExecutionError},
		{name: "step error", evalCase: failedCase("case", MetricSummary{Name: "m", Evaluated: true}, func(c *CaseSummary) {
			c.ActualInvocations = invocation(nil, []RouteStep{{Error: "tool failed"}})
		}), want: FailureExecutionError},
		{name: "route", evalCase: failedCase("case", MetricSummary{Name: "m", Evaluated: true}, func(c *CaseSummary) {
			c.ActualInvocations = invocation(nil, []RouteStep{{Agent: "wrong"}})
			c.ExpectedInvocations = invocation(nil, []RouteStep{{Agent: "right"}})
		}), want: FailureRouteError},
		{name: "tool call", evalCase: failedCase("case", MetricSummary{Name: "m", Evaluated: true}, func(c *CaseSummary) {
			c.ActualInvocations = invocation([]ToolSummary{tool("search", `{}`)}, nil)
			c.ExpectedInvocations = invocation([]ToolSummary{tool("lookup", `{}`)}, nil)
		}), want: FailureToolCallError},
		{name: "tool arguments", evalCase: failedCase("case", MetricSummary{Name: "m", Evaluated: true}, func(c *CaseSummary) {
			c.ActualInvocations = invocation([]ToolSummary{tool("search", `{"q":"go"}`)}, nil)
			c.ExpectedInvocations = invocation([]ToolSummary{tool("search", `{"q":"rust"}`)}, nil)
		}), want: FailureToolArgumentError},
		{name: "tool result", evalCase: failedCase("case", MetricSummary{Name: "m", Evaluated: true}, func(c *CaseSummary) {
			c.ActualInvocations = invocation([]ToolSummary{{Name: "search", Arguments: json.RawMessage(`{"q":"go"}`), Result: json.RawMessage(`{"answer":"wrong"}`)}}, nil)
			c.ExpectedInvocations = invocation([]ToolSummary{{Name: "search", Arguments: json.RawMessage(`{"q":"go"}`), Result: json.RawMessage(`{"answer":"right"}`)}}, nil)
		}), want: FailureToolCallError},
		{name: "format", evalCase: failedCase("case", MetricSummary{
			Name: "judge", Evaluated: true, RubricTypes: []string{"JSON format"}, Reason: "invalid schema",
		}, nil), want: FailureFormatError},
		{name: "knowledge", evalCase: failedCase("case", MetricSummary{
			Name: "grounding", Evaluated: true, Reason: "missing citation",
		}, nil), want: FailureKnowledgeRecall},
		{name: "information is not format", evalCase: failedCase("case", MetricSummary{
			Name: "answer", Evaluated: true, Reason: "missing information from retrieved context",
		}, nil), want: FailureKnowledgeRecall},
		{name: "final response", evalCase: failedCase("case", MetricSummary{
			Name: "answer", Evaluated: true, Criterion: "final_response", Reason: "wrong answer",
		}, nil), want: FailureFinalResponse},
		{name: "fallback", evalCase: failedCase("case", MetricSummary{Name: "custom", Evaluated: true}, nil), want: FailureOtherEvaluationError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attribution, err := Attribute(&EvalSummary{Cases: []CaseSummary{test.evalCase}})
			if err != nil {
				t.Fatalf("Attribute() error = %v", err)
			}
			if got := attribution.Failures[0].Category; got != test.want {
				t.Fatalf("category = %q, want %q", got, test.want)
			}
			if attribution.Failures[0].Reason == "" || attribution.Counts[test.want] != 1 {
				t.Fatalf("attribution = %+v", attribution)
			}
		})
	}
}

func TestAttributeSkipsPassedCasesAndBuildsHints(t *testing.T) {
	summary := &EvalSummary{Cases: []CaseSummary{
		{ID: "passed", Passed: true},
		failedCase("failed", MetricSummary{Name: "answer", Evaluated: true, Criterion: "final_response", Reason: strings.Repeat("x", 800)}, nil),
	}}
	attribution, err := Attribute(summary)
	if err != nil {
		t.Fatalf("Attribute() error = %v", err)
	}
	hints, err := Hints(attribution)
	if err != nil {
		t.Fatalf("Hints() error = %v", err)
	}
	if len(hints) != 1 || hints[0].CaseID != "failed" || len([]rune(hints[0].Reason)) > 530 {
		t.Fatalf("hints = %+v", hints)
	}
}

func TestAttributeSortsCasesAndBuildsOneHintPerFailedMetric(t *testing.T) {
	if _, err := Attribute(nil); err == nil {
		t.Fatal("Attribute(nil) error = nil")
	}
	if _, err := Hints(nil); err == nil {
		t.Fatal("Hints(nil) error = nil")
	}
	summary := &EvalSummary{Cases: []CaseSummary{
		failedCase("z", MetricSummary{Name: "b", Evaluated: true, Reason: strings.Repeat("x", 800)}, nil),
		failedCase("a", MetricSummary{Name: "a", Evaluated: true, Reason: "bad"}, nil),
	}}
	summary.Cases[0].Metrics = append(summary.Cases[0].Metrics, MetricSummary{Name: "a", Evaluated: true, Reason: "also bad"})
	attribution, err := Attribute(summary)
	if err != nil {
		t.Fatal(err)
	}
	hints, err := Hints(attribution)
	if err != nil {
		t.Fatal(err)
	}
	if attribution.Failures[0].CaseID != "a" || len(hints) != 3 || len([]rune(hints[1].Reason)) > 530 {
		t.Fatalf("attribution=%+v hints=%+v", attribution, hints)
	}
}

func TestHintsPreserveMetricSpecificReasons(t *testing.T) {
	summary := &EvalSummary{Cases: []CaseSummary{failedCase("case", MetricSummary{
		Name: "format", Evaluated: true, Reason: "invalid JSON",
	}, nil)}}
	summary.Cases[0].Metrics = append(summary.Cases[0].Metrics, MetricSummary{
		Name: "quality", Evaluated: true, Reason: "missing citation",
	})
	attribution, err := Attribute(summary)
	if err != nil {
		t.Fatal(err)
	}
	hints, err := Hints(attribution)
	if err != nil {
		t.Fatal(err)
	}
	if len(hints) != 2 || hints[0].MetricName != "format" || hints[0].Reason != "invalid JSON" ||
		hints[1].MetricName != "quality" || hints[1].Reason != "missing citation" {
		t.Fatalf("hints = %+v", hints)
	}
}

func TestToolComparisonKeepsArgumentsAndResultsAssociated(t *testing.T) {
	tool := func(argument, result string) ToolSummary {
		return ToolSummary{Name: "search", Arguments: json.RawMessage(argument), Result: json.RawMessage(result)}
	}
	paired := []ToolSummary{tool(`{"q":"a"}`, `1`), tool(`{"q":"b"}`, `2`)}
	tests := []struct {
		name           string
		actual         []ToolSummary
		orderSensitive bool
		wantDifferent  bool
	}{
		{name: "result mismatch", actual: []ToolSummary{tool(`{"q":"a"}`, `9`), tool(`{"q":"b"}`, `2`)}, wantDifferent: true},
		{name: "cross paired", actual: []ToolSummary{tool(`{"q":"a"}`, `2`), tool(`{"q":"b"}`, `1`)}, wantDifferent: true},
		{name: "order insensitive", actual: []ToolSummary{paired[1], paired[0]}},
		{name: "order sensitive", actual: []ToolSummary{paired[1], paired[0]}, orderSensitive: true, wantDifferent: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := []InvocationSummary{{Tools: test.actual}}
			expected := []InvocationSummary{{Tools: paired}}
			got := toolCallsDiffer(actual, expected, test.orderSensitive) ||
				toolArgumentsDiffer(actual, expected, test.orderSensitive) ||
				toolResultsDiffer(actual, expected, test.orderSensitive)
			if got != test.wantDifferent {
				t.Fatalf("trajectory differs = %t, want %t", got, test.wantDifferent)
			}
		})
	}
}

func TestGateAcceptsCandidateAtBoundaries(t *testing.T) {
	delta := validationDelta(0.1, CaseDelta{
		ID: "critical", Kind: DeltaImproved, ScoreDelta: 0.1,
		Metrics: []MetricDelta{{Name: "hard", Kind: DeltaImproved, ScoreDelta: -0.05}},
	})
	decision, err := Gate(GatePolicy{
		MinValidationGain: 0.1, RejectNewHardFail: true, HardMetrics: []string{"hard"},
		CriticalCases: []string{"critical"}, MaxMetricDrop: 0.05, MaxModelCalls: 2, MaxTokens: 100,
	}, delta, Cost{ModelCalls: 2, Tokens: 100})
	if err != nil {
		t.Fatalf("Gate() error = %v", err)
	}
	if !decision.Accepted || len(decision.Reasons) != 0 {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestGateRejectsRegressionsAndBudgets(t *testing.T) {
	delta := validationDelta(-0.1, CaseDelta{
		ID: "critical", Kind: DeltaNewlyFailed, ScoreDelta: -0.2,
		Metrics: []MetricDelta{{Name: "hard", Kind: DeltaNewlyFailed, ScoreDelta: -0.3}},
	})
	decision, err := Gate(GatePolicy{
		RejectNewHardFail: true, HardMetrics: []string{"hard"}, CriticalCases: []string{"critical"},
		MaxMetricDrop: 0.1, MaxModelCalls: 1, MaxTokens: 10,
	}, delta, Cost{ModelCalls: 2, Tokens: 11})
	if err != nil {
		t.Fatalf("Gate() error = %v", err)
	}
	if decision.Accepted || len(decision.Reasons) != 6 {
		t.Fatalf("decision = %+v", decision)
	}
	for _, fragment := range []string{"validation score", "critical case", "hard metric", "dropped", "model calls", "tokens"} {
		if !containsReason(decision.Reasons, fragment) {
			t.Fatalf("reasons %v do not contain %q", decision.Reasons, fragment)
		}
	}
}

func TestGateRejectsInvalidPolicyAndMissingConfiguredNames(t *testing.T) {
	delta := validationDelta(0.1, CaseDelta{ID: "case", Metrics: []MetricDelta{{Name: "metric"}}})
	tests := []GatePolicy{
		{MaxMetricDrop: -1},
		{MaxTokens: -1},
		{MinValidationGain: math.NaN()},
		{MaxMetricDrop: math.Inf(1)},
		{HardMetrics: []string{""}},
		{HardMetrics: []string{"missing"}},
		{CriticalCases: []string{"missing"}},
		{HardMetrics: []string{"metric", "metric"}},
	}
	for _, policy := range tests {
		if _, err := Gate(policy, delta, Cost{}); err == nil {
			t.Fatalf("Gate(%+v) error = nil", policy)
		}
	}
}

func TestGateRejectsNilDeltaAndNegativeCost(t *testing.T) {
	if _, err := Gate(GatePolicy{}, nil, Cost{}); err == nil {
		t.Fatal("Gate(nil) error = nil")
	}
	if _, err := Gate(GatePolicy{}, validationDelta(0), Cost{LatencyMS: -1}); err == nil {
		t.Fatal("Gate(negative cost) error = nil")
	}
}

func TestGateRejectsMetricEvaluationStateChanges(t *testing.T) {
	tests := []struct {
		name   string
		metric MetricDelta
		policy GatePolicy
		want   string
	}{
		{
			name: "hard metric becomes evaluated failure",
			metric: MetricDelta{
				Name: "hard", Kind: DeltaUnchanged, CandidateEvaluated: true, CandidatePassed: false,
			},
			policy: GatePolicy{RejectNewHardFail: true, HardMetrics: []string{"hard"}},
			want:   "hard metric",
		},
		{
			name: "evaluation disappears",
			metric: MetricDelta{
				Name: "quality", Kind: DeltaUnchanged, BaselineEvaluated: true, BaselinePassed: true,
			},
			policy: GatePolicy{},
			want:   "no longer evaluated",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delta := validationDelta(0, CaseDelta{ID: "case", Metrics: []MetricDelta{test.metric}})
			decision, err := Gate(test.policy, delta, Cost{})
			if err != nil {
				t.Fatalf("Gate() error = %v", err)
			}
			if decision.Accepted || !containsReason(decision.Reasons, test.want) {
				t.Fatalf("decision = %+v", decision)
			}
		})
	}
}

func failedCase(id string, metric MetricSummary, modify func(*CaseSummary)) CaseSummary {
	result := CaseSummary{ID: id, Metrics: []MetricSummary{metric}}
	if modify != nil {
		modify(&result)
	}
	return result
}

func validationDelta(scoreDelta float64, cases ...CaseDelta) *DatasetDelta {
	return &DatasetDelta{EvalSetID: "validation", ScoreDelta: scoreDelta, Cases: cases}
}

func containsReason(reasons []string, fragment string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, fragment) {
			return true
		}
	}
	return false
}
