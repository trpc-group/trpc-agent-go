//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package regression compares Evaluation Service results for PromptIter workflows.
package regression

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// EvalSummary is the compact result shared by comparison, gating, and reports.
type EvalSummary struct {
	EvalSetID string        `json:"eval_set_id"`
	Score     float64       `json:"score"`
	Passed    bool          `json:"passed"`
	LatencyMS int64         `json:"latency_ms"`
	Cost      Cost          `json:"cost"`
	Cases     []CaseSummary `json:"cases"`
}

// Cost is the model usage observed in execution traces, including failed runs.
type Cost struct {
	// ModelCalls is populated by a runtime model wrapper; traces cannot provide
	// an exact count because their usage may aggregate several model calls.
	ModelCalls int   `json:"model_calls"`
	Tokens     int64 `json:"tokens"`
	LatencyMS  int64 `json:"latency_ms"`
}

// CaseSummary contains one case's aggregate metrics and attribution evidence.
type CaseSummary struct {
	ID                  string              `json:"id"`
	Score               float64             `json:"score"`
	Passed              bool                `json:"passed"`
	Error               string              `json:"error,omitempty"`
	Metrics             []MetricSummary     `json:"metrics"`
	ActualInvocations   []InvocationSummary `json:"actual_invocations,omitempty"`
	ExpectedInvocations []InvocationSummary `json:"expected_invocations,omitempty"`
}

// MetricSummary contains the aggregate facts needed to compare and explain a metric.
type MetricSummary struct {
	Name               string   `json:"name"`
	Score              float64  `json:"score"`
	Threshold          float64  `json:"threshold"`
	Passed             bool     `json:"passed"`
	Evaluated          bool     `json:"evaluated"`
	Reason             string   `json:"reason,omitempty"`
	Criterion          string   `json:"criterion,omitempty"`
	RubricTypes        []string `json:"rubric_types,omitempty"`
	ToolOrderSensitive bool     `json:"tool_order_sensitive,omitempty"`
}

// InvocationSummary is the bounded execution evidence used by failure attribution.
type InvocationSummary struct {
	FinalResponse string        `json:"final_response,omitempty"`
	Tools         []ToolSummary `json:"tools,omitempty"`
	Route         []RouteStep   `json:"route,omitempty"`
}

// ToolSummary is one tool call in invocation order.
type ToolSummary struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
}

// RouteStep is the stable routing identity of one execution trace step.
type RouteStep struct {
	Agent  string `json:"agent,omitempty"`
	Branch string `json:"branch,omitempty"`
	NodeID string `json:"node_id,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Summarize converts a normal Evaluation Service result into deterministic facts.
func Summarize(result *evaluation.EvaluationResult) (*EvalSummary, error) {
	if result == nil {
		return nil, errors.New("evaluation result is nil")
	}
	if strings.TrimSpace(result.EvalSetID) == "" {
		return nil, errors.New("evaluation result has no eval set id")
	}
	if result.ExecutionTime < 0 {
		return nil, errors.New("evaluation result has negative execution time")
	}
	cases := append([]*evaluation.EvaluationCaseResult(nil), result.EvalCases...)
	sort.Slice(cases, func(i, j int) bool {
		if cases[i] == nil {
			return true
		}
		if cases[j] == nil {
			return false
		}
		return cases[i].EvalCaseID < cases[j].EvalCaseID
	})
	summary := &EvalSummary{EvalSetID: result.EvalSetID, LatencyMS: result.ExecutionTime.Milliseconds()}
	var total float64
	var metricCount int
	for i, evalCase := range cases {
		if evalCase == nil || strings.TrimSpace(evalCase.EvalCaseID) == "" {
			return nil, fmt.Errorf("evaluation case at index %d is nil or has no id", i)
		}
		if i > 0 && cases[i-1].EvalCaseID == evalCase.EvalCaseID {
			return nil, fmt.Errorf("duplicate evaluation case %q", evalCase.EvalCaseID)
		}
		caseSummary, score, count, err := summarizeCase(evalCase)
		if err != nil {
			return nil, fmt.Errorf("summarize case %q: %w", evalCase.EvalCaseID, err)
		}
		summary.Cases = append(summary.Cases, caseSummary)
		addCaseCost(&summary.Cost, evalCase)
		total += score
		metricCount += count
	}
	if metricCount == 0 {
		return nil, errors.New("evaluation result has no evaluated metrics")
	}
	summary.Score = total / float64(metricCount)
	summary.Passed = len(summary.Cases) > 0
	for _, evalCase := range summary.Cases {
		summary.Passed = summary.Passed && evalCase.Passed
	}
	return summary, nil
}

func summarizeCase(evalCase *evaluation.EvaluationCaseResult) (CaseSummary, float64, int, error) {
	summary := CaseSummary{ID: evalCase.EvalCaseID}
	for _, run := range evalCase.EvalCaseResults {
		if run != nil && strings.TrimSpace(run.ErrorMessage) != "" {
			summary.Error = run.ErrorMessage
			break
		}
	}
	if summary.Error == "" {
		for _, detail := range evalCase.RunDetails {
			if detail == nil || detail.Inference == nil {
				continue
			}
			if detail.Inference.ErrorMessage != "" {
				summary.Error = detail.Inference.ErrorMessage
			} else if detail.Inference.Status == status.EvalStatusFailed {
				summary.Error = "inference failed"
			}
			for _, executionTrace := range detail.Inference.ExecutionTraces {
				if summary.Error == "" && traceExecutionFailed(executionTrace) {
					summary.Error = "execution trace failed"
				}
			}
		}
	}

	metrics := append([]*evalresult.EvalMetricResult(nil), evalCase.MetricResults...)
	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i] == nil {
			return true
		}
		if metrics[j] == nil {
			return false
		}
		return metrics[i].MetricName < metrics[j].MetricName
	})
	var total float64
	var count int
	allPassed := summary.Error == ""
	for i, metric := range metrics {
		if metric == nil || strings.TrimSpace(metric.MetricName) == "" {
			return CaseSummary{}, 0, 0, fmt.Errorf("metric at index %d is nil or has no name", i)
		}
		if i > 0 && metrics[i-1].MetricName == metric.MetricName {
			return CaseSummary{}, 0, 0, fmt.Errorf("duplicate metric %q", metric.MetricName)
		}
		if !finite(metric.Score) || !finite(metric.Threshold) {
			return CaseSummary{}, 0, 0, fmt.Errorf("metric %q has a non-finite score or threshold", metric.MetricName)
		}
		item := summarizeMetric(metric, evalCase.EvalCaseResults)
		summary.Metrics = append(summary.Metrics, item)
		if !item.Evaluated {
			continue
		}
		total += item.Score
		count++
		allPassed = allPassed && item.Passed
	}
	if count == 0 {
		return CaseSummary{}, 0, 0, errors.New("case has no evaluated metrics")
	}
	summary.Score = total / float64(count)
	summary.Passed = allPassed
	summary.ActualInvocations, summary.ExpectedInvocations = invocationEvidence(evalCase)
	return summary, total, count, nil
}

func summarizeMetric(metric *evalresult.EvalMetricResult, runs []*evalresult.EvalCaseResult) MetricSummary {
	result := MetricSummary{
		Name:      metric.MetricName,
		Score:     metric.Score,
		Threshold: metric.Threshold,
		Passed:    metric.EvalStatus == status.EvalStatusPassed,
		Evaluated: metric.EvalStatus != status.EvalStatusNotEvaluated,
	}
	if metric.Details != nil {
		result.Reason = metric.Details.Reason
		if result.Reason == "" {
			for _, rubric := range metric.Details.RubricScores {
				if rubric != nil && rubric.Reason != "" {
					result.Reason = rubric.Reason
					break
				}
			}
		}
	}
	if result.Reason == "" && !result.Passed && result.Evaluated {
		result.Reason = metricReason(runs, metric.MetricName)
	}
	if metric.Criterion == nil {
		return result
	}
	switch {
	case metric.Criterion.ToolTrajectory != nil:
		result.Criterion = "tool_trajectory"
		result.ToolOrderSensitive = metric.Criterion.ToolTrajectory.OrderSensitive
	case metric.Criterion.FinalResponse != nil:
		result.Criterion = "final_response"
	case metric.Criterion.LLMJudge != nil:
		result.Criterion = "llm_judge"
		for _, rubric := range metric.Criterion.LLMJudge.Rubrics {
			if rubric != nil && rubric.Type != "" {
				result.RubricTypes = append(result.RubricTypes, rubric.Type)
			}
		}
		sort.Strings(result.RubricTypes)
	}
	return result
}

func metricReason(runs []*evalresult.EvalCaseResult, name string) string {
	for _, run := range runs {
		if run == nil {
			continue
		}
		for _, invocation := range run.EvalMetricResultPerInvocation {
			if invocation == nil {
				continue
			}
			for _, metric := range invocation.EvalMetricResults {
				if metric == nil || metric.MetricName != name || metric.Details == nil {
					continue
				}
				if metric.Details.Reason != "" {
					return metric.Details.Reason
				}
				for _, rubric := range metric.Details.RubricScores {
					if rubric != nil && rubric.Reason != "" {
						return rubric.Reason
					}
				}
			}
		}
	}
	return "metric failed without evaluator reason"
}

func invocationEvidence(evalCase *evaluation.EvaluationCaseResult) ([]InvocationSummary, []InvocationSummary) {
	runs := evalCase.EvalCaseResults
	runs = append([]*evalresult.EvalCaseResult(nil), runs...)
	sort.Slice(runs, func(i, j int) bool {
		if runs[i] == nil {
			return true
		}
		if runs[j] == nil {
			return false
		}
		return runs[i].RunID < runs[j].RunID
	})
	var actual, expected []InvocationSummary
	for _, run := range runs {
		if run == nil {
			continue
		}
		for _, pair := range run.EvalMetricResultPerInvocation {
			if pair == nil {
				continue
			}
			actual = appendInvocation(actual, pair.ActualInvocation, traceForInvocation(evalCase.RunDetails, run.RunID, pair.ActualInvocation))
			expected = appendInvocation(expected, pair.ExpectedInvocation)
		}
	}
	return actual, expected
}

func appendInvocation(dst []InvocationSummary, invocation *evalset.Invocation, executionTrace ...*trace.Trace) []InvocationSummary {
	if invocation == nil {
		return dst
	}
	item := InvocationSummary{}
	if invocation.FinalResponse != nil {
		item.FinalResponse = messageText(invocation.FinalResponse)
	}
	for _, call := range invocation.Tools {
		if call == nil {
			continue
		}
		arguments := safeJSON(call.Arguments)
		tool := ToolSummary{Name: call.Name, Arguments: arguments}
		if call.Result != nil {
			tool.Result = safeJSON(call.Result)
		}
		item.Tools = append(item.Tools, tool)
	}
	if invocation.FinalResponse != nil {
		for _, call := range invocation.FinalResponse.ToolCalls {
			item.Tools = append(item.Tools, ToolSummary{Name: call.Function.Name, Arguments: safeJSON(call.Function.Arguments)})
		}
	}
	selectedTrace := invocation.ExecutionTrace
	if len(executionTrace) > 0 && executionTrace[0] != nil {
		selectedTrace = executionTrace[0]
	}
	item.Route = summarizeTrace(selectedTrace)
	return append(dst, item)
}

func traceForInvocation(details []*evaluation.EvaluationCaseRunDetails, runID int, invocation *evalset.Invocation) *trace.Trace {
	for _, detail := range details {
		if detail == nil || detail.RunID != runID || detail.Inference == nil {
			continue
		}
		for _, candidate := range detail.Inference.ExecutionTraces {
			if candidate == nil {
				continue
			}
			if invocation != nil && invocation.InvocationID != "" && candidate.RootInvocationID == invocation.InvocationID {
				return candidate
			}
		}
		if len(detail.Inference.ExecutionTraces) == 1 {
			return detail.Inference.ExecutionTraces[0]
		}
	}
	return nil
}

func messageText(message *model.Message) string {
	parts := make([]string, 0, 1+len(message.ContentParts))
	if message.Content != "" {
		parts = append(parts, message.Content)
	}
	for _, part := range message.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			parts = append(parts, *part.Text)
		} else {
			parts = append(parts, "["+string(part.Type)+"]")
		}
	}
	return truncate(strings.Join(parts, "\n"), 4096)
}

func safeJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`null`)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return json.RawMessage(`null`)
	}
	encoded, err := json.Marshal(redactValue(decoded))
	if err != nil {
		return json.RawMessage(`null`)
	}
	if len(encoded) > 16<<10 {
		hash := sha256.Sum256(encoded)
		encoded, _ = json.Marshal(map[string]any{"truncated": true, "sha256": fmt.Sprintf("%x", hash), "bytes": len(encoded)})
	}
	return encoded
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "... [truncated]"
}

func redactValue(value any) any {
	switch item := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(item))
		for key, child := range item {
			if sensitiveKey(key) {
				redacted[key] = "<redacted>"
			} else {
				redacted[key] = redactValue(child)
			}
		}
		return redacted
	case []any:
		redacted := make([]any, len(item))
		for index, child := range item {
			redacted[index] = redactValue(child)
		}
		return redacted
	default:
		return value
	}
}

func sensitiveKey(key string) bool {
	normalized := strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			return character
		}
		if character >= 'A' && character <= 'Z' {
			return character + ('a' - 'A')
		}
		return -1
	}, key)
	for _, fragment := range []string{"authorization", "apikey", "token", "password", "secret", "cookie", "privatekey", "credential"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func addCaseCost(cost *Cost, evalCase *evaluation.EvaluationCaseResult) {
	traceFound := false
	for _, detail := range evalCase.RunDetails {
		if detail == nil || detail.Inference == nil {
			continue
		}
		for _, executionTrace := range detail.Inference.ExecutionTraces {
			if executionTrace == nil {
				continue
			}
			traceFound = true
			addTraceCost(cost, executionTrace)
		}
	}
	if traceFound {
		return
	}
	for _, run := range evalCase.EvalCaseResults {
		if run == nil {
			continue
		}
		for _, invocation := range run.EvalMetricResultPerInvocation {
			if invocation != nil && invocation.ActualInvocation != nil && invocation.ActualInvocation.ExecutionTrace != nil {
				addTraceCost(cost, invocation.ActualInvocation.ExecutionTrace)
			}
		}
	}
}

func addTraceCost(cost *Cost, executionTrace *trace.Trace) {
	if executionTrace.Usage != nil {
		cost.Tokens += int64(executionTrace.Usage.TotalTokens)
	}
	if !executionTrace.StartedAt.IsZero() && !executionTrace.EndedAt.IsZero() {
		duration := executionTrace.EndedAt.Sub(executionTrace.StartedAt)
		if duration > 0 {
			cost.LatencyMS += duration.Milliseconds()
		}
	}
	if executionTrace.Usage == nil {
		for _, step := range executionTrace.Steps {
			if step.Usage != nil {
				cost.Tokens += int64(step.Usage.TotalTokens)
			}
		}
	}
}

func summarizeTrace(executionTrace *trace.Trace) []RouteStep {
	if executionTrace == nil {
		return nil
	}
	result := make([]RouteStep, 0, len(executionTrace.Steps))
	for _, step := range executionTrace.Steps {
		result = append(result, RouteStep{
			Agent: step.AgentName, Branch: step.Branch, NodeID: step.NodeID, Error: step.Error,
		})
	}
	return result
}

func traceExecutionFailed(executionTrace *trace.Trace) bool {
	if executionTrace == nil {
		return false
	}
	if executionTrace.Status == trace.TraceStatusFailed || executionTrace.Status == trace.TraceStatusIncomplete {
		return true
	}
	for _, step := range executionTrace.Steps {
		if step.Error != "" {
			return true
		}
	}
	return false
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
