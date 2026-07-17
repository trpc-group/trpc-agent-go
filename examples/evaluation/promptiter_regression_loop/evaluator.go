//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type localEvaluator struct {
	metrics []MetricInput
	engine  FakeEngineConfig
}

func newLocalEvaluator(metrics []MetricInput, engine FakeEngineConfig) *localEvaluator {
	return &localEvaluator{metrics: metrics, engine: engine}
}

func (e *localEvaluator) Evaluate(ctx context.Context, name string, set EvalSetInput, prompt string) (EvaluationRun, error) {
	start := time.Now()
	run := EvaluationRun{
		Name:      name,
		EvalSetID: set.EvalSetID,
		Cases:     make([]CaseResult, 0, len(set.Cases)),
	}
	total := 0.0
	for _, evalCase := range set.Cases {
		if err := ctx.Err(); err != nil {
			return EvaluationRun{}, err
		}
		caseStart := time.Now()
		result, err := e.evaluateCase(set.EvalSetID, evalCase, prompt)
		if err != nil {
			return EvaluationRun{}, err
		}
		result.LatencyMs = time.Since(caseStart).Milliseconds()
		run.Cases = append(run.Cases, result)
		total += result.Score
		if result.Status == status.EvalStatusPassed {
			run.Passed++
		} else {
			run.Failed++
		}
		run.Cost = addCost(run.Cost, estimateCaseCost(prompt, result.Actual))
	}
	if len(run.Cases) > 0 {
		run.OverallScore = total / float64(len(run.Cases))
	}
	run.LatencyMs = time.Since(start).Milliseconds()
	return run, nil
}

func (e *localEvaluator) evaluateCase(evalSetID string, evalCase EvalCase, prompt string) (CaseResult, error) {
	if len(evalCase.Conversation) == 0 {
		return CaseResult{}, fmt.Errorf("eval case %s has no conversation", evalCase.EvalID)
	}
	expected := evalCase.Conversation[0]
	actual := fakeInvocation(evalCase, expected, prompt)
	metricResults := make([]MetricResult, 0, len(e.metrics))
	total := 0.0
	for _, metric := range e.metrics {
		result := scoreMetric(metric, evalCase, expected, actual)
		metricResults = append(metricResults, result)
		total += result.Score
	}
	score := 0.0
	if len(metricResults) > 0 {
		score = total / float64(len(metricResults))
	}
	statusValue := status.EvalStatusPassed
	for _, metric := range metricResults {
		if metric.Status != status.EvalStatusPassed {
			statusValue = status.EvalStatusFailed
			break
		}
	}
	failures := AttributeFailures(metricResults, actual, expected)
	trace := TraceSummary{
		Mode:           "deterministic_trace",
		Route:          routeForInvocation(actual),
		ToolTrajectory: actual.Tools,
		Signals:        traceSignals(actual, expected, failures),
	}
	return CaseResult{
		EvalSetID:      evalSetID,
		CaseID:         evalCase.EvalID,
		Critical:       evalCase.Critical,
		Score:          score,
		Status:         statusValue,
		Metrics:        metricResults,
		FailureReasons: failures,
		Expected:       expected,
		Actual:         actual,
		Trace:          trace,
	}, nil
}

func scoreMetric(metric MetricInput, evalCase EvalCase, expected, actual Invocation) MetricResult {
	threshold := metric.Threshold
	if threshold == 0 {
		threshold = 1
	}
	score := 1.0
	reason := ""
	switch metric.MetricName {
	case "final_response_exact", "llm_rubric_critic":
		if !sameText(messageText(expected.FinalResponse), messageText(actual.FinalResponse)) {
			score = 0
			if strings.Contains(evalCase.EvalID, "policy") {
				reason = "knowledge recall gap: refund policy window is missing or wrong"
			} else if strings.Contains(evalCase.EvalID, "json") {
				reason = fmt.Sprintf("format error: expected structured response %q, actual %q",
					messageText(expected.FinalResponse), messageText(actual.FinalResponse))
			} else {
				reason = fmt.Sprintf("final response mismatch: expected %q, actual %q",
					messageText(expected.FinalResponse), messageText(actual.FinalResponse))
			}
		}
	case "tool_trajectory_exact", "tool_trajectory_avg_score":
		score, reason = scoreToolTrajectory(expected.Tools, actual.Tools)
	case "format_json":
		score, reason = scoreJSONFormat(expected, actual)
	default:
		score = 1
	}
	statusValue := status.EvalStatusPassed
	if score < threshold {
		statusValue = status.EvalStatusFailed
	}
	return MetricResult{
		MetricName: metric.MetricName,
		Score:      score,
		Threshold:  threshold,
		Status:     statusValue,
		Reason:     reason,
	}
}

func scoreToolTrajectory(expected, actual []ToolCall) (float64, string) {
	switch {
	case len(expected) == 0 && len(actual) == 0:
		return 1, ""
	case len(expected) == 0 && len(actual) > 0:
		return 0, "route error: expected direct answer but tool was called"
	case len(expected) > 0 && len(actual) == 0:
		return 0, "tool call error: expected a tool call but no tool was called"
	case len(expected) != len(actual):
		return 0, "tool call error: tool call count does not match expectation"
	}
	for i := range expected {
		if expected[i].Name != actual[i].Name {
			return 0, "tool call error: tool name does not match expectation"
		}
		if !jsonEqual(expected[i].Arguments, actual[i].Arguments) {
			return 0, "tool argument error: tool arguments do not match expectation"
		}
		if !jsonEqual(expected[i].Result, actual[i].Result) {
			return 0, "tool call error: tool result does not match expectation"
		}
	}
	return 1, ""
}

func scoreJSONFormat(expected, actual Invocation) (float64, string) {
	expectedText := strings.TrimSpace(messageText(expected.FinalResponse))
	actualText := strings.TrimSpace(messageText(actual.FinalResponse))
	expectedJSON := strings.HasPrefix(expectedText, "{")
	actualJSON := strings.HasPrefix(actualText, "{")
	if expectedJSON {
		var actualObj map[string]any
		if err := json.Unmarshal([]byte(actualText), &actualObj); err != nil {
			return 0, "format error: expected JSON object but actual response is not valid JSON"
		}
		if !sameText(expectedText, actualText) {
			return 0, "format error: JSON response does not match expected schema or values"
		}
		return 1, ""
	}
	if actualJSON {
		return 0, "format error: direct natural-language answer should not be JSON"
	}
	return 1, ""
}

func fakeInvocation(evalCase EvalCase, expected Invocation, prompt string) Invocation {
	actual := expected
	actual.InvocationID = evalCase.EvalID + "-actual"
	actual.Tools = append([]ToolCall(nil), expected.Tools...)
	strictJSON := strings.Contains(prompt, "STRICT_JSON_OUTPUT")
	preciseTools := strings.Contains(prompt, "EXTRACT_TOOL_ARGUMENTS")
	overfitJSON := strings.Contains(prompt, "OVERFIT_JSON_ALL_DIRECT_ANSWERS")
	switch evalCase.EvalID {
	case "train_json_invoice":
		if !strictJSON {
			actual.FinalResponse = assistant("The invoice is approved for 120 USD.")
		}
	case "train_weather_paris":
		if !preciseTools {
			actual.Tools = []ToolCall{{
				ID:        "tool_use_weather_wrong",
				Name:      "lookup_weather",
				Arguments: map[string]any{"city": "France", "date": "today"},
				Result:    map[string]any{"city": "France", "condition": "unknown", "temperature_c": 0},
			}}
			actual.FinalResponse = assistant("I could not identify the exact Paris weather record.")
		}
	case "train_refund_policy":
		actual.FinalResponse = assistant("Standard refunds are available within 14 days.")
	case "val_json_refund":
		if !strictJSON {
			actual.FinalResponse = assistant("Refund request r-204 is approved for 35 USD.")
		}
	case "val_weather_berlin":
		// Baseline already handles this case; the optimized prompt must preserve it.
	case "val_critical_direct_status":
		if overfitJSON {
			actual.FinalResponse = assistant(`{"flight":"TR900","status":"boarding","gate":"K12"}`)
		}
	}
	return actual
}

func assistant(content string) *Message {
	return &Message{Role: "assistant", Content: content}
}

func messageText(message *Message) string {
	if message == nil {
		return ""
	}
	return strings.TrimSpace(message.Content)
}

func sameText(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

func jsonEqual(a, b any) bool {
	aj, err := json.Marshal(a)
	if err != nil {
		return reflect.DeepEqual(a, b)
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return reflect.DeepEqual(a, b)
	}
	var ao any
	var bo any
	if err := json.Unmarshal(aj, &ao); err != nil {
		return reflect.DeepEqual(a, b)
	}
	if err := json.Unmarshal(bj, &bo); err != nil {
		return reflect.DeepEqual(a, b)
	}
	return reflect.DeepEqual(ao, bo)
}

func routeForInvocation(invocation Invocation) string {
	if len(invocation.Tools) == 0 {
		return "direct_response"
	}
	return "tool_augmented_response"
}

func traceSignals(actual, expected Invocation, failures []FailureAttribution) []string {
	signals := []string{routeForInvocation(actual)}
	if len(actual.Tools) > 0 {
		signals = append(signals, "tool_trajectory_recorded")
	}
	if len(failures) == 0 {
		signals = append(signals, "all_metrics_passed")
		return signals
	}
	for _, failure := range failures {
		signals = append(signals, failure.Category)
	}
	if len(expected.Tools) == 0 && len(actual.Tools) > 0 {
		signals = append(signals, "unexpected_tool_route")
	}
	return signals
}

func estimateCaseCost(prompt string, actual Invocation) CostSummary {
	promptTokens := estimateTokens(prompt)
	completionTokens := estimateTokens(messageText(actual.FinalResponse))
	for _, tool := range actual.Tools {
		payload, _ := json.Marshal(tool)
		completionTokens += estimateTokens(string(payload))
	}
	return CostSummary{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalCalls:       1,
		EstimatedUSD:     float64(promptTokens+completionTokens) * 0.0000002,
	}
}

func estimateTokens(text string) int {
	fields := strings.Fields(text)
	if len(fields) == 0 && strings.TrimSpace(text) != "" {
		return 1
	}
	return len(fields)
}

func addCost(a, b CostSummary) CostSummary {
	return CostSummary{
		PromptTokens:     a.PromptTokens + b.PromptTokens,
		CompletionTokens: a.CompletionTokens + b.CompletionTokens,
		TotalCalls:       a.TotalCalls + b.TotalCalls,
		EstimatedUSD:     a.EstimatedUSD + b.EstimatedUSD,
	}
}
