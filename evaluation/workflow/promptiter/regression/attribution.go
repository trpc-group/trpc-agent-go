//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regression

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// Stable failure attribution codes.
const (
	FailureFinalResponseMismatch = "final_response_mismatch"
	FailureToolCallError         = "tool_call_error"
	FailureToolArgumentError     = "tool_argument_error"
	FailureToolExecutionError    = "tool_execution_error"
	FailureRoutingError          = "routing_error"
	FailureFormatError           = "format_error"
	FailureKnowledgeGap          = "knowledge_gap"
	FailureExecutionError        = "execution_error"
	FailureEvaluationMismatch    = "evaluation_mismatch"
)

// FailureReason is one structured, explainable failure attribution.
type FailureReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Metric  string `json:"metric,omitempty"`
}

// CaseAttribution groups all identified reasons for one failed case.
type CaseAttribution struct {
	CaseID  string          `json:"caseId"`
	Reasons []FailureReason `json:"reasons"`
}

// AttributionOptions supplies optional expected routing evidence.
type AttributionOptions struct {
	ExpectedAgentName  string
	ExpectedAgentNames map[string]string
}

// AttributeFailures classifies every failed case using structured evidence first.
func AttributeFailures(result *engine.EvaluationResult, options AttributionOptions) []CaseAttribution {
	if result == nil {
		return []CaseAttribution{}
	}
	out := make([]CaseAttribution, 0)
	for _, set := range result.EvalSets {
		for _, evalCase := range set.Cases {
			expectedAgent := options.ExpectedAgentName
			if value, ok := options.ExpectedAgentNames[evalCase.EvalCaseID]; ok {
				expectedAgent = value
			}
			reasons := attributeCase(evalCase, expectedAgent)
			if len(reasons) > 0 {
				out = append(out, CaseAttribution{CaseID: evalCase.EvalCaseID, Reasons: reasons})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CaseID < out[j].CaseID })
	return out
}

func attributeCase(evalCase engine.CaseResult, expectedAgent string) []FailureReason {
	failedMetrics := make([]engine.MetricResult, 0)
	for _, metric := range evalCase.Metrics {
		if metric.Status == status.EvalStatusFailed {
			failedMetrics = append(failedMetrics, metric)
		}
	}
	if len(failedMetrics) == 0 {
		return nil
	}
	reasons := structuredInvocationReasons(evalCase, failedMetrics)
	if expectedAgent != "" && evalCase.Trace != nil {
		for _, step := range evalCase.Trace.Steps {
			if step.AgentName != "" && step.AgentName != expectedAgent {
				reasons = append(reasons, FailureReason{Code: FailureRoutingError, Message: fmt.Sprintf("expected agent %q, got %q", expectedAgent, step.AgentName)})
				break
			}
		}
	}
	if evalCase.Trace != nil {
		for _, step := range evalCase.Trace.Steps {
			if step.Error != "" {
				reasons = append(reasons, FailureReason{Code: FailureExecutionError, Message: step.Error})
				break
			}
		}
	}
	for _, metric := range failedMetrics {
		code, message := classifyMetric(metric.MetricName, metric.Reason)
		reasons = append(reasons, FailureReason{Code: code, Message: message, Metric: metric.MetricName})
	}
	reasons = deduplicateReasons(reasons)
	if len(reasons) == 0 {
		return []FailureReason{{Code: FailureEvaluationMismatch, Message: "evaluation failed without a specific structured signal"}}
	}
	return reasons
}

func structuredInvocationReasons(evalCase engine.CaseResult, failed []engine.MetricResult) []FailureReason {
	if len(evalCase.ActualInvocations) == 0 || len(evalCase.ExpectedInvocations) == 0 {
		return nil
	}
	count := min(len(evalCase.ActualInvocations), len(evalCase.ExpectedInvocations))
	reasons := make([]FailureReason, 0)
	for i := 0; i < count; i++ {
		actual, expected := evalCase.ActualInvocations[i], evalCase.ExpectedInvocations[i]
		if metricFailed(failed, "final", "response", "rouge") && !messagesEqual(actual, expected) {
			reasons = append(reasons, FailureReason{Code: FailureFinalResponseMismatch, Message: "actual final response differs from the expected response"})
		}
		if metricFailed(failed, "json", "schema", "format") && invalidJSONResponse(actual) {
			reasons = append(reasons, FailureReason{Code: FailureFormatError, Message: "actual final response is not valid JSON"})
		}
		if metricFailed(failed, "tool") {
			reasons = append(reasons, compareTools(actual, expected)...)
		}
	}
	return reasons
}

func messagesEqual(actual, expected *evalset.Invocation) bool {
	if actual == nil || expected == nil {
		return actual == expected
	}
	return reflect.DeepEqual(actual.FinalResponse, expected.FinalResponse)
}

func invalidJSONResponse(actual *evalset.Invocation) bool {
	if actual == nil || actual.FinalResponse == nil {
		return true
	}
	return !json.Valid([]byte(actual.FinalResponse.Content))
}

func compareTools(actual, expected *evalset.Invocation) []FailureReason {
	if actual == nil || expected == nil {
		return []FailureReason{{Code: FailureToolCallError, Message: "actual or expected invocation is missing"}}
	}
	if len(actual.Tools) != len(expected.Tools) {
		return []FailureReason{{Code: FailureToolCallError, Message: fmt.Sprintf("expected %d tool calls, got %d", len(expected.Tools), len(actual.Tools))}}
	}
	reasons := make([]FailureReason, 0)
	for i := range actual.Tools {
		actualTool, expectedTool := actual.Tools[i], expected.Tools[i]
		if actualTool == nil || expectedTool == nil || actualTool.Name != expectedTool.Name {
			reasons = append(reasons, FailureReason{Code: FailureToolCallError, Message: fmt.Sprintf("tool call %d has an unexpected name", i)})
			continue
		}
		if !reflect.DeepEqual(actualTool.Arguments, expectedTool.Arguments) {
			reasons = append(reasons, FailureReason{Code: FailureToolArgumentError, Message: fmt.Sprintf("tool %q arguments differ from expected", actualTool.Name)})
		}
		if message := toolExecutionError(actualTool.Result); message != "" {
			reasons = append(reasons, FailureReason{Code: FailureToolExecutionError, Message: message})
		}
	}
	return reasons
}

func toolExecutionError(result any) string {
	if result == nil {
		return ""
	}
	switch value := result.(type) {
	case error:
		return value.Error()
	case map[string]any:
		for _, key := range []string{"error", "error_message", "errorMessage"} {
			if message, ok := value[key].(string); ok && strings.TrimSpace(message) != "" {
				return message
			}
		}
	}
	return ""
}

func metricFailed(metrics []engine.MetricResult, fragments ...string) bool {
	for _, metric := range metrics {
		name := strings.ToLower(metric.MetricName)
		for _, fragment := range fragments {
			if strings.Contains(name, fragment) {
				return true
			}
		}
	}
	return false
}

func classifyMetric(metricName, rawReason string) (string, string) {
	if code, message, ok := structuredReason(rawReason); ok {
		return normalizeReasonCode(code), message
	}
	name := strings.ToLower(metricName)
	reason := strings.ToLower(rawReason)
	switch {
	case strings.Contains(name, "json_schema") || strings.Contains(name, "schema") || strings.Contains(reason, "invalid json"):
		return FailureFormatError, fallbackMessage(rawReason, "structured output does not match the required format")
	case strings.Contains(name, "tool") && (strings.Contains(reason, "argument") || strings.Contains(reason, "parameter")):
		return FailureToolArgumentError, fallbackMessage(rawReason, "tool arguments do not match the expected call")
	case strings.Contains(name, "tool") && strings.Contains(reason, "error"):
		return FailureToolExecutionError, fallbackMessage(rawReason, "tool execution failed")
	case strings.Contains(name, "tool"):
		return FailureToolCallError, fallbackMessage(rawReason, "tool trajectory does not match the expected call")
	case strings.Contains(name, "final") || strings.Contains(name, "rouge"):
		return FailureFinalResponseMismatch, fallbackMessage(rawReason, "final response does not match expectations")
	case strings.Contains(name, "retrieve") || strings.Contains(name, "retrieval") || strings.Contains(name, "knowledge") || strings.Contains(reason, "knowledge"):
		return FailureKnowledgeGap, fallbackMessage(rawReason, "required knowledge was not retrieved")
	case strings.Contains(reason, "route") || strings.Contains(reason, "agent"):
		return FailureRoutingError, fallbackMessage(rawReason, "an unexpected route handled the request")
	default:
		return FailureEvaluationMismatch, fallbackMessage(rawReason, fmt.Sprintf("metric %q failed without a more specific signal", metricName))
	}
}

func structuredReason(raw string) (string, string, bool) {
	var value struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(raw), &value) != nil || strings.TrimSpace(value.Code) == "" {
		return "", "", false
	}
	return value.Code, fallbackMessage(value.Message, raw), true
}

func normalizeReasonCode(code string) string {
	if code == "tool_arg_error" {
		return FailureToolArgumentError
	}
	return code
}

func fallbackMessage(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func deduplicateReasons(in []FailureReason) []FailureReason {
	seen := make(map[string]struct{}, len(in))
	out := make([]FailureReason, 0, len(in))
	for _, reason := range in {
		key := reason.Code + "\x00" + reason.Metric
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, reason)
	}
	return out
}
