//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const testSemanticPrompt = "test"

func TestLocalEvaluatorFailureAttributionDeterminismAndUsage(t *testing.T) {
	metrics := []MetricConfig{
		{MetricName: metricFinalResponse, Threshold: 0.99, Weight: 1},
		{MetricName: metricToolTrajectory, Threshold: 0.99, Weight: 1},
		{MetricName: metricRoute, Threshold: 0.99, Weight: 1},
		{MetricName: metricStructuredOutput, Threshold: 0.99, Weight: 1},
		{MetricName: metricKnowledgeRecall, Threshold: 0.99, Weight: 1},
	}
	evaluator, err := NewLocalEvaluator(metrics, "baseline")
	if err != nil {
		t.Fatalf("NewLocalEvaluator() error = %v", err)
	}

	valid := false
	set := &EvalSet{
		EvalSetID:     "six-failure-categories",
		PassThreshold: testScore(0.99),
		EvalCases: []EvalCase{
			newFailureCase(
				"final-response",
				"expected answer",
				nil,
				Expectations{},
				FakeOutput{
					Response: "different answer",
					Usage: Usage{
						ModelCalls: 1, InputTokens: 10, OutputTokens: 1,
						CostUSD: 0.01, LatencyMS: 10,
					},
				},
			),
			newFailureCase(
				"tool-call",
				"done",
				[]*evalset.Tool{newTool("weather", "Paris", "sunny")},
				Expectations{},
				FakeOutput{
					Response: "done",
					Tools:    []*evalset.Tool{newTool("search", "Paris", "sunny")},
					Usage: Usage{
						ModelCalls: 2, InputTokens: 20, OutputTokens: 2,
						CostUSD: 0.02, LatencyMS: 20,
					},
				},
			),
			newFailureCase(
				"tool-parameter",
				"done",
				[]*evalset.Tool{newTool("weather", "Paris", "sunny")},
				Expectations{},
				FakeOutput{
					Response: "done",
					Tools:    []*evalset.Tool{newTool("weather", "London", "sunny")},
					Usage: Usage{
						ModelCalls: 3, ToolCalls: 1, InputTokens: 30, OutputTokens: 3,
						CostUSD: 0.03, LatencyMS: 30,
					},
				},
			),
			newFailureCase(
				"route",
				"done",
				nil,
				Expectations{Route: "specialist"},
				FakeOutput{
					Response: "done",
					Route:    "generalist",
					Usage: Usage{
						ModelCalls: 4, InputTokens: 40, OutputTokens: 4,
						CostUSD: 0.04, LatencyMS: 40,
					},
				},
			),
			newFailureCase(
				"format",
				`{"ok":true}`,
				nil,
				Expectations{ResponseFormat: "json"},
				FakeOutput{
					Response:        `{"ok":true}`,
					StructuredValid: &valid,
					Usage: Usage{
						ModelCalls: 5, InputTokens: 50, OutputTokens: 5,
						CostUSD: 0.05, LatencyMS: 50,
					},
				},
			),
			newFailureCase(
				"knowledge",
				"done",
				nil,
				Expectations{
					RequiredFacts:         []string{"fact-a", "fact-b"},
					MinRetrievedDocuments: 2,
				},
				FakeOutput{
					Response:           "done",
					RetrievedFacts:     []string{"fact-a"},
					RetrievedDocuments: 1,
					Usage: Usage{
						ModelCalls: 6, InputTokens: 60, OutputTokens: 6,
						CostUSD: 0.06, LatencyMS: 60,
					},
				},
			),
		},
	}

	testPrompt := testSemanticPrompt + "\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
	first, err := evaluator.Evaluate(context.Background(), set, "candidate", testPrompt)
	if err != nil {
		t.Fatalf("Evaluate() first run error = %v", err)
	}
	second, err := evaluator.Evaluate(context.Background(), set, "candidate", testPrompt)
	if err != nil {
		t.Fatalf("Evaluate() second run error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Evaluate() is not deterministic:\nfirst:  %#v\nsecond: %#v", first, second)
	}

	wantCategory := map[string]FailureCategory{
		"final-response": FailureFinalResponseMismatch,
		"tool-call":      FailureToolCallError,
		"tool-parameter": FailureToolParameterError,
		"route":          FailureRouteError,
		"format":         FailureFormatError,
		"knowledge":      FailureKnowledgeRetrievalInsufficient,
	}
	if got, want := first.FailedCases, len(wantCategory); got != want {
		t.Fatalf("FailedCases = %d, want %d", got, want)
	}
	if first.PassedCases != 0 {
		t.Fatalf("PassedCases = %d, want 0", first.PassedCases)
	}
	for _, result := range first.Cases {
		want, ok := wantCategory[result.CaseID]
		if !ok {
			t.Fatalf("unexpected case %q", result.CaseID)
		}
		if result.Passed {
			t.Errorf("case %q unexpectedly passed", result.CaseID)
		}
		if len(result.FailureAttributions) == 0 {
			t.Errorf("case %q has no failure attribution", result.CaseID)
			continue
		}
		if got := result.FailureAttributions[0].Category; got != want {
			t.Errorf("case %q category = %q, want %q", result.CaseID, got, want)
		}
		for _, attribution := range result.FailureAttributions {
			if strings.TrimSpace(attribution.Evidence) == "" {
				t.Errorf("case %q attribution %q has no evidence", result.CaseID, attribution.Category)
			}
		}
		failedMetrics := 0
		for _, metricResult := range result.MetricResults {
			if metricResult.Passed {
				continue
			}
			failedMetrics++
			if strings.TrimSpace(metricResult.Reason) == "" {
				t.Errorf("case %q failed metric %q has no reason", result.CaseID, metricResult.MetricName)
			}
		}
		if failedMetrics == 0 {
			t.Errorf("case %q failed without a failed metric", result.CaseID)
		}
	}
	for _, category := range wantCategory {
		if got := first.AttributionStats[category]; got != 1 {
			t.Errorf("AttributionStats[%q] = %d, want 1", category, got)
		}
	}

	wantUsage := Usage{
		ModelCalls:   21,
		ToolCalls:    2,
		InputTokens:  210,
		OutputTokens: 21,
		CostUSD:      0.21,
		LatencyMS:    210,
	}
	assertUsageEqual(t, first.Usage, wantUsage)
	if got := first.Cases[1].Usage.ToolCalls; got != 1 {
		t.Errorf("automatically derived tool calls = %d, want 1", got)
	}
}

func TestAttributeFailureAlwaysExplainsFailedCase(t *testing.T) {
	attributions := AttributeFailure(CaseResult{Passed: false})
	if len(attributions) == 0 {
		t.Fatal("AttributeFailure() returned no reason for a failed case")
	}
	if strings.TrimSpace(attributions[0].Evidence) == "" {
		t.Fatal("AttributeFailure() returned an empty fallback reason")
	}
	if got := AttributeFailure(CaseResult{Passed: true}); got != nil {
		t.Fatalf("AttributeFailure() for passed case = %#v, want nil", got)
	}
}

func newFailureCase(
	id string,
	expectedResponse string,
	expectedTools []*evalset.Tool,
	expectations Expectations,
	output FakeOutput,
) EvalCase {
	output.PromptSemanticSHA256 = HashText(testSemanticPrompt)
	return EvalCase{
		EvalID: id,
		Conversation: []*evalset.Invocation{{
			InvocationID: id + "-invocation",
			FinalResponse: &model.Message{
				Role:    model.RoleAssistant,
				Content: expectedResponse,
			},
			Tools: expectedTools,
		}},
		Expectations: expectations,
		FakeResponses: map[string]FakeOutput{
			"candidate": output,
		},
	}
}

func newTool(name, city, result string) *evalset.Tool {
	return &evalset.Tool{
		Name:      name,
		Arguments: map[string]any{"city": city},
		Result:    map[string]any{"weather": result},
	}
}

func assertUsageEqual(t *testing.T, got, want Usage) {
	t.Helper()
	if got.ModelCalls != want.ModelCalls ||
		got.ToolCalls != want.ToolCalls ||
		got.InputTokens != want.InputTokens ||
		got.OutputTokens != want.OutputTokens ||
		got.LatencyMS != want.LatencyMS ||
		math.Abs(got.CostUSD-want.CostUSD) > 1e-12 {
		t.Errorf("usage = %+v, want %+v", got, want)
	}
}

func testScore(value float64) *float64 {
	return &value
}

func TestLocalEvaluatorWrongToolNameCannotEarnArgumentOrResultCredit(t *testing.T) {
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricToolTrajectory,
		Threshold:  0.5,
		Weight:     1,
		HardFail:   true,
	}}, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	set := &EvalSet{
		EvalSetID:     "wrong-tool-name",
		PassThreshold: testScore(0.5),
		EvalCases: []EvalCase{newFailureCase(
			"wrong-name",
			"done",
			[]*evalset.Tool{{Name: "weather"}},
			Expectations{},
			FakeOutput{
				Response: "done",
				Tools:    []*evalset.Tool{{Name: "delete_user"}},
				Usage:    Usage{ModelCalls: 1},
			},
		)},
	}
	prompt := "test\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
	summary, err := evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	if err != nil {
		t.Fatal(err)
	}
	result := summary.Cases[0]
	if result.Score != 0 || result.Passed {
		t.Fatalf("wrong tool score/pass = %v/%v, want 0/false", result.Score, result.Passed)
	}
	if result.PrimaryFailure == nil || result.PrimaryFailure.Category != FailureToolCallError {
		t.Fatalf("primary failure = %+v, want tool call error", result.PrimaryFailure)
	}
}

func TestLocalEvaluatorOmittedRouteArgumentsAndResultAreUnconstrained(t *testing.T) {
	evaluator, err := NewLocalEvaluator([]MetricConfig{
		{MetricName: metricToolTrajectory, Threshold: 1, Weight: 1},
		{MetricName: metricRoute, Threshold: 1, Weight: 1},
	}, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	set := &EvalSet{
		EvalSetID:     "optional-signals",
		PassThreshold: testScore(1),
		EvalCases: []EvalCase{newFailureCase(
			"optional",
			"done",
			[]*evalset.Tool{{Name: "weather"}},
			Expectations{},
			FakeOutput{
				Response: "done",
				Route:    "weather-agent",
				Tools: []*evalset.Tool{{
					Name:      "weather",
					Arguments: map[string]any{"city": "Shenzhen"},
					Result:    map[string]any{"temperature": 30},
				}},
				Usage: Usage{ModelCalls: 1},
			},
		)},
	}
	prompt := "test\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
	summary, err := evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Cases[0].Passed || summary.Cases[0].Score != 1 {
		t.Fatalf("optional signals should not constrain output: %+v", summary.Cases[0])
	}
}

func TestLocalEvaluatorTraceToolTimeoutIsHardFailure(t *testing.T) {
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricFinalResponse,
		Threshold:  1,
		Weight:     1,
	}}, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	set := &EvalSet{
		EvalSetID:     "trace-error",
		PassThreshold: testScore(1),
		EvalCases: []EvalCase{newFailureCase(
			"timeout",
			"done",
			nil,
			Expectations{},
			FakeOutput{
				Response: "done",
				Trace: []TraceStep{{
					StepID: "tool-timeout",
					Kind:   "tool",
					Status: "timeout",
				}},
				Usage: Usage{ModelCalls: 1},
			},
		)},
	}
	prompt := "test\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
	summary, err := evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	if err != nil {
		t.Fatal(err)
	}
	result := summary.Cases[0]
	if result.Passed || !result.HardFail || result.PrimaryFailure == nil || result.PrimaryFailure.Category != FailureToolCallError {
		t.Fatalf("trace timeout was not attributed as a hard tool failure: %+v", result)
	}
}

func TestLocalEvaluatorTraceModeRequiresAndReplaysRecordedTrace(t *testing.T) {
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricFinalResponse,
		Threshold:  1,
		Weight:     1,
	}}, "baseline", "trace")
	if err != nil {
		t.Fatal(err)
	}
	set := &EvalSet{
		EvalSetID:     "trace-replay",
		PassThreshold: testScore(1),
		EvalCases: []EvalCase{newFailureCase(
			"trace-case",
			"recorded answer",
			nil,
			Expectations{},
			FakeOutput{Response: "recorded answer", Usage: Usage{ModelCalls: 1, LatencyMS: 1}},
		)},
	}
	prompt := "test\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
	_, err = evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	if err == nil || !strings.Contains(err.Error(), "has no recorded trace") {
		t.Fatalf("trace mode missing-trace error = %v", err)
	}
	output := set.EvalCases[0].FakeResponses["candidate"]
	elapsed := int64(1)
	output.Trace = []TraceStep{{
		StepID:    "replay-1",
		Kind:      "final_response",
		Status:    "completed",
		ElapsedMS: &elapsed,
		Message:   "recorded answer",
		Usage:     &output.Usage,
	}}
	set.EvalCases[0].FakeResponses["candidate"] = output
	summary, err := evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Cases[0].Passed || summary.Cases[0].FinalResponse != "recorded answer" {
		t.Fatalf("recorded trace was not replayed: %+v", summary.Cases[0])
	}
}

func TestLocalEvaluatorFakeModeRejectsContradictoryTrace(t *testing.T) {
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricFinalResponse,
		Threshold:  1,
		Weight:     1,
	}}, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	set := &EvalSet{
		EvalSetID:     "fake-trace-consistency",
		PassThreshold: testScore(1),
		EvalCases: []EvalCase{newFailureCase(
			"case",
			"recorded answer",
			nil,
			Expectations{},
			FakeOutput{
				Response: "recorded answer",
				Trace: []TraceStep{{
					StepID:  "llm-1",
					Kind:    "llm",
					Status:  "completed",
					Message: "contradictory answer",
				}},
				Usage: Usage{ModelCalls: 1},
			},
		)},
	}
	prompt := "test\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
	_, err = evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	if err == nil || !strings.Contains(err.Error(), "does not match output response") {
		t.Fatalf("contradictory trace error = %v", err)
	}
}

func TestLocalEvaluatorRejectsUnknownTraceStatus(t *testing.T) {
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricFinalResponse,
		Threshold:  1,
		Weight:     1,
	}}, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	set := &EvalSet{
		EvalSetID:     "trace-status",
		PassThreshold: testScore(1),
		EvalCases: []EvalCase{newFailureCase(
			"case",
			"answer",
			nil,
			Expectations{},
			FakeOutput{
				Response: "answer",
				Trace:    []TraceStep{{StepID: "llm-1", Kind: "llm", Status: "finished"}},
				Usage:    Usage{ModelCalls: 1},
			},
		)},
	}
	prompt := "test\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
	_, err = evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	if err == nil || !strings.Contains(err.Error(), "unknown status") {
		t.Fatalf("unknown trace status error = %v", err)
	}
}

func TestLocalEvaluatorRejectsMultipleYAMLDocuments(t *testing.T) {
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricStructuredOutput,
		Threshold:  1,
		Weight:     1,
		HardFail:   true,
	}}, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	set := &EvalSet{
		EvalSetID:     "yaml-single-document",
		PassThreshold: testScore(1),
		EvalCases: []EvalCase{newFailureCase(
			"case",
			"",
			nil,
			Expectations{ResponseFormat: "yaml"},
			FakeOutput{Response: "answer: one\n---\nanswer: two", Usage: Usage{ModelCalls: 1}},
		)},
	}
	prompt := "test\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
	summary, err := evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	if err != nil {
		t.Fatal(err)
	}
	result := summary.Cases[0]
	if result.Passed || result.StructuredValid || result.PrimaryFailure == nil || result.PrimaryFailure.Category != FailureFormatError {
		t.Fatalf("multiple YAML documents were not rejected: %+v", result)
	}
}

func TestLocalEvaluatorExecutionErrorCannotPassZeroThreshold(t *testing.T) {
	evaluator, err := NewLocalEvaluator([]MetricConfig{{
		MetricName: metricFinalResponse,
		Threshold:  0,
		Weight:     1,
	}}, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	set := &EvalSet{
		EvalSetID:     "execution-error",
		PassThreshold: testScore(0),
		EvalCases: []EvalCase{newFailureCase(
			"case",
			"",
			nil,
			Expectations{},
			FakeOutput{Error: "model unavailable", Usage: Usage{ModelCalls: 1}},
		)},
	}
	prompt := "test\n\n[[trpc-promptiter-candidate:candidate;seed:1]]"
	summary, err := evaluator.Evaluate(context.Background(), set, "candidate", prompt)
	if err != nil {
		t.Fatal(err)
	}
	result := summary.Cases[0]
	if result.Passed || !result.HardFail || result.MetricResults[0].Passed || result.MetricResults[0].Weight != 1 {
		t.Fatalf("execution error incorrectly passed: %+v", result)
	}
}

func TestAttributionDoesNotTreatExplicitlyCorrectRouteAsRouteFailure(t *testing.T) {
	tests := []struct {
		name     string
		reason   string
		notWrong FailureCategory
	}{
		{name: "route", reason: "The route was correct; only the final answer was wrong.", notWrong: FailureRouteError},
		{name: "tool", reason: "The tool call was correct; only the final answer was wrong.", notWrong: FailureToolCallError},
		{name: "parameter", reason: "The parameters were correct; only the final answer was wrong.", notWrong: FailureToolParameterError},
		{name: "format", reason: "The JSON was valid; only the final answer was wrong.", notWrong: FailureFormatError},
		{name: "knowledge", reason: "Retrieval was sufficient; only the final answer was wrong.", notWrong: FailureKnowledgeRetrievalInsufficient},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := CaseResult{
				Passed:       false,
				RubricReason: test.reason,
				MetricResults: []MetricResult{
					{MetricName: metricFinalResponse, Score: 0, Threshold: 1, Weight: 1, Passed: false},
					{MetricName: metricLLMRubric, Score: 0, Threshold: 1, Weight: 1, Passed: false},
				},
			}
			attributions := AttributeFailure(result)
			if len(attributions) == 0 || attributions[0].Category != FailureFinalResponseMismatch {
				t.Fatalf("primary attribution = %+v, want final-response mismatch", attributions)
			}
			for _, attribution := range attributions {
				if attribution.Category == test.notWrong {
					t.Fatalf("explicitly correct %s was misattributed: %+v", test.name, attributions)
				}
			}
		})
	}
}
