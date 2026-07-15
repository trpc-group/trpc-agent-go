//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

type structuredToolCall struct {
	Name      string
	Arguments any
	Result    any
}

func classifyStructuredFailure(
	metricName string,
	reason string,
	trace *atrace.Trace,
	actualInvocation *evalset.Invocation,
	expectedInvocation *evalset.Invocation,
	metricSignal FailureCategory,
) (FailureCategory, float64, bool) {
	text := strings.ToLower(metricName + " " + reason)
	actualTools := toolCallsFromInvocation(actualInvocation)
	if len(actualTools) == 0 {
		actualTools = toolCallsFromTrace(trace)
	}
	expectedTools := toolCallsFromInvocation(expectedInvocation)
	if isToolStructuredSignal(text, metricSignal, actualTools, expectedTools) {
		if category, ok := classifyToolDiff(actualTools, expectedTools); ok {
			return category, 0.92, true
		}
	}

	actualFinal := finalResponseText(actualInvocation)
	if actualFinal == "" {
		actualFinal = finalTraceText(trace)
	}
	expectedFinal := finalResponseText(expectedInvocation)
	if isFormatStructuredSignal(text, metricSignal) {
		if formatMismatch(actualFinal, expectedFinal) {
			return FailureFormatError, 0.9, true
		}
	}

	if isRouteStructuredSignal(text, metricSignal) {
		if routeMismatch(actualInvocation, expectedInvocation, trace) {
			return FailureRouteError, 0.88, true
		}
	}

	if isKnowledgeStructuredSignal(text, metricSignal) {
		if finalResponseMismatch(actualFinal, expectedFinal) {
			return FailureKnowledgeRecallGap, 0.82, true
		}
	}

	if isFinalResponseStructuredSignal(text, metricSignal) {
		if finalResponseMismatch(actualFinal, expectedFinal) {
			return FailureFinalResponseMismatch, 0.82, true
		}
	}
	if metricSignal == FailureUnknown && genericFailureReason(reason) {
		if category, ok := classifyToolDiff(actualTools, expectedTools); ok {
			return category, 0.88, true
		}
		if routeMismatch(actualInvocation, expectedInvocation, trace) {
			return FailureRouteError, 0.84, true
		}
		if formatMismatch(actualFinal, expectedFinal) {
			return FailureFormatError, 0.84, true
		}
		if finalResponseMismatch(actualFinal, expectedFinal) {
			return FailureFinalResponseMismatch, 0.78, true
		}
	}
	return "", 0, false
}

func structuredFailureCategories(
	metricName string,
	reason string,
	trace *atrace.Trace,
	actualInvocation *evalset.Invocation,
	expectedInvocation *evalset.Invocation,
	metricSignal FailureCategory,
) []FailureCategory {
	text := strings.ToLower(metricName + " " + reason)
	actualTools := toolCallsFromInvocation(actualInvocation)
	if len(actualTools) == 0 {
		actualTools = toolCallsFromTrace(trace)
	}
	expectedTools := toolCallsFromInvocation(expectedInvocation)
	actualFinal := finalResponseText(actualInvocation)
	if actualFinal == "" {
		actualFinal = finalTraceText(trace)
	}
	expectedFinal := finalResponseText(expectedInvocation)

	var categories []FailureCategory
	if isToolStructuredSignal(text, metricSignal, actualTools, expectedTools) {
		if category, ok := classifyToolDiff(actualTools, expectedTools); ok {
			categories = append(categories, category)
		}
	}
	if isFormatStructuredSignal(text, metricSignal) && formatMismatch(actualFinal, expectedFinal) {
		categories = append(categories, FailureFormatError)
	}
	if isRouteStructuredSignal(text, metricSignal) && routeMismatch(actualInvocation, expectedInvocation, trace) {
		categories = append(categories, FailureRouteError)
	}
	if isKnowledgeStructuredSignal(text, metricSignal) && finalResponseMismatch(actualFinal, expectedFinal) {
		categories = append(categories, FailureKnowledgeRecallGap)
	}
	if isFinalResponseStructuredSignal(text, metricSignal) && finalResponseMismatch(actualFinal, expectedFinal) {
		categories = append(categories, FailureFinalResponseMismatch)
	}
	if metricSignal == FailureUnknown && genericFailureReason(reason) {
		if category, ok := classifyToolDiff(actualTools, expectedTools); ok {
			categories = append(categories, category)
		}
		if routeMismatch(actualInvocation, expectedInvocation, trace) {
			categories = append(categories, FailureRouteError)
		}
		if formatMismatch(actualFinal, expectedFinal) {
			categories = append(categories, FailureFormatError)
		}
		if finalResponseMismatch(actualFinal, expectedFinal) {
			categories = append(categories, FailureFinalResponseMismatch)
		}
	}
	return dedupeCategoriesExcluding(FailureUnknown, categories...)
}

func structuredEvidence(actualInvocation, expectedInvocation *evalset.Invocation) []string {
	var evidence []string
	actualTools := toolCallsFromInvocation(actualInvocation)
	expectedTools := toolCallsFromInvocation(expectedInvocation)
	if len(actualTools) > 0 {
		evidence = append(evidence, "actual_tools="+strings.Join(toolNames(actualTools), ","))
	}
	if len(expectedTools) > 0 {
		evidence = append(evidence, "expected_tools="+strings.Join(toolNames(expectedTools), ","))
	}
	if text := finalResponseText(actualInvocation); text != "" {
		evidence = append(evidence, "actual_final_response="+trimForEvidence(text))
	}
	if text := finalResponseText(expectedInvocation); text != "" {
		evidence = append(evidence, "expected_final_response="+trimForEvidence(text))
	}
	return evidence
}

func isToolStructuredSignal(
	text string,
	metricSignal FailureCategory,
	actualTools []structuredToolCall,
	expectedTools []structuredToolCall,
) bool {
	if metricSignal == FailureToolCallError || metricSignal == FailureToolArgumentError {
		return true
	}
	if len(expectedTools) > 0 {
		return true
	}
	if len(actualTools) > 0 || len(expectedTools) > 0 {
		return containsAny(text, "tool", "trajectory", "function", "api call", "工具")
	}
	return false
}

func isFormatStructuredSignal(text string, metricSignal FailureCategory) bool {
	return metricSignal == FailureFormatError ||
		containsAny(text, "json", "xml", "schema", "format", "structured", "字段", "格式")
}

func isRouteStructuredSignal(text string, metricSignal FailureCategory) bool {
	return metricSignal == FailureRouteError ||
		containsAny(text, "route", "router", "handoff", "sub-agent", "subagent", "agent selection", "子代理", "路由")
}

func isKnowledgeStructuredSignal(text string, metricSignal FailureCategory) bool {
	return metricSignal == FailureKnowledgeRecallGap ||
		containsAny(text, "knowledge", "recall", "grounding", "citation", "source", "fact", "policy", "召回", "引用", "事实")
}

func isFinalResponseStructuredSignal(text string, metricSignal FailureCategory) bool {
	return metricSignal == FailureFinalResponseMismatch ||
		containsAny(text, "final response", "final_response", "rouge", "answer", "expected", "actual", "答案", "回复")
}

func classifyToolDiff(actualTools, expectedTools []structuredToolCall) (FailureCategory, bool) {
	if len(expectedTools) == 0 {
		if len(actualTools) > 0 {
			return FailureToolCallError, true
		}
		return "", false
	}
	if len(actualTools) == 0 {
		return FailureToolCallError, true
	}
	actualNames := toolNameCounts(actualTools)
	expectedNames := toolNameCounts(expectedTools)
	if !reflect.DeepEqual(actualNames, expectedNames) {
		return FailureToolCallError, true
	}
	for i, expected := range expectedTools {
		actual := actualTools[i]
		if actual.Name != expected.Name {
			return FailureToolCallError, true
		}
		if !jsonValuesEqual(actual.Arguments, expected.Arguments) {
			return FailureToolArgumentError, true
		}
		if expected.Result != nil && !jsonValuesEqual(actual.Result, expected.Result) {
			return FailureToolArgumentError, true
		}
	}
	return "", false
}

func toolCallsFromInvocation(invocation *evalset.Invocation) []structuredToolCall {
	if invocation == nil {
		return nil
	}
	out := make([]structuredToolCall, 0, len(invocation.Tools))
	for _, tool := range invocation.Tools {
		if tool == nil {
			continue
		}
		out = append(out, structuredToolCall{
			Name:      strings.TrimSpace(tool.Name),
			Arguments: normalizeJSONLike(tool.Arguments),
			Result:    normalizeJSONLike(tool.Result),
		})
	}
	return out
}

func toolCallsFromTrace(trace *atrace.Trace) []structuredToolCall {
	if trace == nil {
		return nil
	}
	var out []structuredToolCall
	for _, step := range trace.Steps {
		out = append(out, toolCallsFromSnapshot(step.Input)...)
		out = append(out, toolCallsFromSnapshot(step.Output)...)
	}
	return dedupeToolCalls(out)
}

func toolCallsFromSnapshot(snapshot *atrace.Snapshot) []structuredToolCall {
	if snapshot == nil || strings.TrimSpace(snapshot.Text) == "" {
		return nil
	}
	var value any
	if err := json.Unmarshal([]byte(snapshot.Text), &value); err != nil {
		return nil
	}
	var out []structuredToolCall
	extractToolCalls(value, &out)
	return out
}

func extractToolCalls(value any, out *[]structuredToolCall) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			extractToolCalls(item, out)
		}
	case map[string]any:
		if calls, ok := typed["tool_calls"].([]any); ok {
			for _, rawCall := range calls {
				if call, ok := toolCallFromMap(rawCall); ok {
					*out = append(*out, call)
				}
			}
		}
		if call, ok := toolCallFromMap(typed); ok {
			*out = append(*out, call)
		}
		for _, child := range typed {
			extractToolCalls(child, out)
		}
	}
}

func toolCallFromMap(value any) (structuredToolCall, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return structuredToolCall{}, false
	}
	name := stringField(object, "tool_name")
	if name == "" {
		name = stringField(object, "name")
	}
	args := firstExistingField(object, "arguments", "args", "input")
	result := firstExistingField(object, "result", "output", "content")
	if function, ok := object["function"].(map[string]any); ok {
		if functionName := stringField(function, "name"); functionName != "" {
			name = functionName
		}
		if args == nil {
			args = firstExistingField(function, "arguments", "args", "input")
		}
	}
	if strings.TrimSpace(name) == "" {
		return structuredToolCall{}, false
	}
	return structuredToolCall{
		Name:      strings.TrimSpace(name),
		Arguments: normalizeJSONLike(args),
		Result:    normalizeJSONLike(result),
	}, true
}

func formatMismatch(actualFinal, expectedFinal string) bool {
	actualFinal = strings.TrimSpace(actualFinal)
	expectedFinal = strings.TrimSpace(expectedFinal)
	if actualFinal == "" {
		return false
	}
	expectedLooksJSON := expectedFinal == "" || json.Valid([]byte(expectedFinal)) || strings.HasPrefix(expectedFinal, "{")
	if expectedLooksJSON && !json.Valid([]byte(actualFinal)) {
		return true
	}
	var actualObject map[string]any
	var expectedObject map[string]any
	if json.Unmarshal([]byte(actualFinal), &actualObject) != nil ||
		json.Unmarshal([]byte(expectedFinal), &expectedObject) != nil {
		return false
	}
	for key := range expectedObject {
		if _, ok := actualObject[key]; !ok {
			return true
		}
	}
	return false
}

func routeMismatch(actualInvocation, expectedInvocation *evalset.Invocation, trace *atrace.Trace) bool {
	actualRoute := routeIDsFromTrace(trace)
	if actualInvocation != nil && actualInvocation.ExecutionTrace != nil {
		actualRoute = routeIDsFromTrace(actualInvocation.ExecutionTrace)
	}
	expectedRoute := []string(nil)
	if expectedInvocation != nil && expectedInvocation.ExecutionTrace != nil {
		expectedRoute = routeIDsFromTrace(expectedInvocation.ExecutionTrace)
	}
	if len(actualRoute) == 0 || len(expectedRoute) == 0 {
		return false
	}
	return !sameStringSet(actualRoute, expectedRoute)
}

func finalResponseMismatch(actualFinal, expectedFinal string) bool {
	actualFinal = normalizeComparableText(actualFinal)
	expectedFinal = normalizeComparableText(expectedFinal)
	return actualFinal != "" && expectedFinal != "" && actualFinal != expectedFinal
}

func finalResponseText(invocation *evalset.Invocation) string {
	if invocation == nil || invocation.FinalResponse == nil {
		return ""
	}
	return strings.TrimSpace(invocation.FinalResponse.Content)
}

func routeIDsFromTrace(trace *atrace.Trace) []string {
	if trace == nil {
		return nil
	}
	var ids []string
	if strings.TrimSpace(trace.RootAgentName) != "" {
		ids = append(ids, "agent:"+strings.TrimSpace(trace.RootAgentName))
	}
	for _, step := range trace.Steps {
		if strings.TrimSpace(step.AgentName) != "" {
			ids = append(ids, "agent:"+strings.TrimSpace(step.AgentName))
		}
		if strings.TrimSpace(step.Branch) != "" {
			ids = append(ids, "branch:"+strings.TrimSpace(step.Branch))
		}
		if strings.TrimSpace(step.NodeID) != "" {
			ids = append(ids, "node:"+strings.TrimSpace(step.NodeID))
		}
	}
	return uniqueStrings(ids)
}

func toolNameCounts(tools []structuredToolCall) map[string]int {
	counts := make(map[string]int)
	for _, tool := range tools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			counts[name]++
		}
	}
	return counts
}

func toolNames(tools []structuredToolCall) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) != "" {
			names = append(names, strings.TrimSpace(tool.Name))
		}
	}
	sort.Strings(names)
	return names
}

func dedupeToolCalls(tools []structuredToolCall) []structuredToolCall {
	seen := make(map[string]struct{}, len(tools))
	out := make([]structuredToolCall, 0, len(tools))
	for _, tool := range tools {
		key := fmt.Sprintf("%s/%v/%v", tool.Name, tool.Arguments, tool.Result)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, tool)
	}
	return out
}

func jsonValuesEqual(actual, expected any) bool {
	return reflect.DeepEqual(normalizeJSONLike(actual), normalizeJSONLike(expected))
}

func normalizeJSONLike(value any) any {
	switch typed := value.(type) {
	case string:
		var decoded any
		if err := json.Unmarshal([]byte(typed), &decoded); err == nil {
			return normalizeJSONLike(decoded)
		}
		return strings.TrimSpace(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = normalizeJSONLike(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = normalizeJSONLike(child)
		}
		return out
	default:
		return typed
	}
}

func stringField(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return strings.TrimSpace(value)
}

func firstExistingField(object map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := object[key]; ok {
			return value
		}
	}
	return nil
}

func normalizeComparableText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.Join(strings.Fields(text), " ")
	return strings.Trim(text, " \t\r\n\"'`.,;:!?")
}

func sameStringSet(left, right []string) bool {
	return reflect.DeepEqual(uniqueStrings(left), uniqueStrings(right))
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
