//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package attribution classifies failures from structured evaluation evidence.
package attribution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

// Rules is a deterministic evidence-first failure classifier.
type Rules struct{}

// NewRules creates a rule-based attributor.
func NewRules() *Rules {
	return &Rules{}
}

// Attribute returns one primary, explainable failure category for a case.
func (r *Rules) Attribute(_ context.Context, result *regression.CaseResult) (*regression.AttributionResult, error) {
	if result == nil {
		return nil, errors.New("case result is nil")
	}
	if strings.TrimSpace(result.CaseID) == "" {
		return nil, errors.New("case id is empty")
	}
	for runIndex, run := range result.Runs {
		for toolIndex, tool := range run.Tools {
			if strings.TrimSpace(tool.Error) == "" {
				continue
			}
			return attributed(result.EvalSetID, result.CaseID, regression.FailureToolResultHandling,
				"structured tool evidence records an execution failure",
				"tool", fmt.Sprintf("runs[%d].tools[%d].error", runIndex, toolIndex), tool.Error), nil
		}
		if run.Error != "" {
			if toolExecutionFailure(run.Error, run.Tools) {
				return attributed(result.EvalSetID, result.CaseID, regression.FailureToolResultHandling,
					"execution evidence identifies a tool invocation failure",
					"execution", fmt.Sprintf("runs[%d].error", runIndex), run.Error), nil
			}
			return attributed(result.EvalSetID, result.CaseID, regression.FailureInferenceError,
				"case execution failed before a reliable quality judgment",
				"execution", fmt.Sprintf("runs[%d].error", runIndex), run.Error), nil
		}
		for stepIndex, step := range run.Trace {
			if step.Error == "" {
				continue
			}
			if toolExecutionFailure(step.Error, run.Tools) {
				return attributed(result.EvalSetID, result.CaseID, regression.FailureToolResultHandling,
					"an execution trace identifies a tool invocation failure",
					"trace", fmt.Sprintf("runs[%d].trace[%d].error", runIndex, stepIndex), step.Error), nil
			}
			return attributed(result.EvalSetID, result.CaseID, regression.FailureInferenceError,
				"an execution trace step failed before a reliable quality judgment",
				"trace", fmt.Sprintf("runs[%d].trace[%d].error", runIndex, stepIndex), step.Error), nil
		}
	}

	failed := make([]classifiedMetric, 0, len(result.Metrics))
	for _, metric := range result.Metrics {
		if metric.Passed {
			continue
		}
		failed = append(failed, classifiedMetric{
			metric:   metric,
			category: metricCategory(metric),
		})
	}
	sort.SliceStable(failed, func(i, j int) bool {
		left := categoryPriority(failed[i].category)
		right := categoryPriority(failed[j].category)
		if left != right {
			return left < right
		}
		return failed[i].metric.Name < failed[j].metric.Name
	})
	structured := classifyExecutionEvidence(result)
	if structured != nil && (len(failed) == 0 ||
		categoryPriority(structured.Category) < categoryPriority(failed[0].category)) {
		return structured, nil
	}
	if len(failed) > 0 {
		selected := failed[0]
		reason := strings.TrimSpace(selected.metric.Reason)
		if reason == "" {
			reason = firstRubricReason(selected.metric)
		}
		if reason == "" {
			reason = fmt.Sprintf("metric %q failed", selected.metric.Name)
		}
		return attributed(result.EvalSetID, result.CaseID, selected.category, reason,
			"metric", "metrics."+selected.metric.Name, reason), nil
	}
	return attributed(result.EvalSetID, result.CaseID, regression.FailureUnknown,
		"case failed without structured metric evidence",
		"case", "passed", "false"), nil
}

func classifyExecutionEvidence(result *regression.CaseResult) *regression.AttributionResult {
	for runIndex, run := range result.Runs {
		if toolIndex, category, reason := compareToolTrajectory(run.ExpectedTools, run.Tools); category != regression.FailureUnknown {
			return attributed(result.EvalSetID, result.CaseID, category, reason,
				"tool_trajectory", fmt.Sprintf("runs[%d].tools[%d]", runIndex, toolIndex), reason)
		}
		if expected, actual := strings.TrimSpace(run.ExpectedRoute), strings.TrimSpace(run.Route); expected != "" && expected != actual {
			reason := fmt.Sprintf("expected route %q, observed %q", expected, actual)
			return attributed(result.EvalSetID, result.CaseID, regression.FailureRoute, reason,
				"trace", fmt.Sprintf("runs[%d].route", runIndex), reason)
		}
		expected := strings.TrimSpace(run.ExpectedFinalResponse)
		actual := strings.TrimSpace(run.FinalResponse)
		if expected == "" || expected == actual {
			continue
		}
		category := regression.FailureFinalResponseMismatch
		reason := "final response differs from expected response"
		if validJSON(expected) && !validJSON(actual) {
			category = regression.FailureFormat
			reason = "final response does not satisfy the expected structured-output format"
		}
		return attributed(result.EvalSetID, result.CaseID, category, reason,
			"final_response", fmt.Sprintf("runs[%d].finalResponse", runIndex), reason)
	}
	return nil
}

func compareToolTrajectory(
	expected []regression.ToolObservation,
	actual []regression.ToolObservation,
) (int, regression.FailureCategory, string) {
	if len(expected) == 0 {
		return 0, regression.FailureUnknown, ""
	}
	if len(expected) != len(actual) {
		return 0, regression.FailureToolSelection,
			fmt.Sprintf("expected %d tool calls, observed %d", len(expected), len(actual))
	}
	for index := range expected {
		if expected[index].Name != actual[index].Name {
			return index, regression.FailureToolSelection,
				fmt.Sprintf("expected tool %q, observed %q", expected[index].Name, actual[index].Name)
		}
		if !sameJSONValue(expected[index].Arguments, actual[index].Arguments) {
			return index, regression.FailureToolArgument,
				fmt.Sprintf("tool %q arguments differ from expected arguments", actual[index].Name)
		}
	}
	return 0, regression.FailureUnknown, ""
}

func sameJSONValue(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == right {
		return true
	}
	var leftValue any
	var rightValue any
	if json.Unmarshal([]byte(left), &leftValue) != nil || json.Unmarshal([]byte(right), &rightValue) != nil {
		return false
	}
	leftJSON, _ := json.Marshal(leftValue)
	rightJSON, _ := json.Marshal(rightValue)
	return string(leftJSON) == string(rightJSON)
}

func validJSON(value string) bool {
	var decoded any
	return json.Unmarshal([]byte(value), &decoded) == nil
}

type classifiedMetric struct {
	metric   regression.MetricResult
	category regression.FailureCategory
}

type taxonomyEntry struct {
	category          regression.FailureCategory
	inferencePriority int
	exactMetricNames  []string
	metricNameSignals []string
	metricNameAll     [][]string
	evidenceSignals   []string
}

// failureTaxonomy is the single ordered definition of supported failure
// categories. Its order is also the precedence used when evidence conflicts.
var failureTaxonomy = []taxonomyEntry{
	{
		category:          regression.FailureSafetyPolicy,
		inferencePriority: 0,
		exactMetricNames:  []string{"safety"},
		metricNameSignals: []string{"safety", "privacy", "安全", "隐私"},
		evidenceSignals: []string{"safety", "unsafe", "privacy", "private data", "policy violation",
			"安全", "不安全", "隐私", "泄露", "违规", "有害"},
	},
	{
		category:          regression.FailureToolSelection,
		inferencePriority: 3,
		exactMetricNames:  []string{"tool_selection"},
		metricNameSignals: []string{"tool", "工具"},
		evidenceSignals:   []string{"tool selection", "wrong tool", "tool call", "工具选择", "错误工具", "工具调用"},
	},
	{
		category:          regression.FailureToolArgument,
		inferencePriority: 1,
		exactMetricNames:  []string{"tool_arguments"},
		metricNameSignals: []string{"工具参数", "参数错误"},
		metricNameAll:     [][]string{{"tool", "argument"}},
		evidenceSignals: []string{"tool argument", "tool parameter", "arguments mismatch", "parameter",
			"工具参数", "参数错误", "参数缺失"},
	},
	{
		category:          regression.FailureToolResultHandling,
		inferencePriority: 2,
		metricNameSignals: []string{"工具结果", "结果处理"},
		metricNameAll:     [][]string{{"tool", "result"}},
		evidenceSignals: []string{"tool result", "tool output", "result mismatch", "result was ignored", "result handling",
			"工具结果", "结果处理"},
	},
	{
		category:          regression.FailureRoute,
		inferencePriority: 4,
		exactMetricNames:  []string{"route"},
		metricNameSignals: []string{"route", "router", "路由"},
		evidenceSignals:   []string{"route", "router", "transfer", "sub-agent", "路由", "子代理", "转交"},
	},
	{
		category:          regression.FailureFormat,
		inferencePriority: 5,
		exactMetricNames:  []string{"format"},
		metricNameSignals: []string{"format", "structured", "格式"},
		evidenceSignals:   []string{"json", "xml", "schema", "format", "structured output", "格式", "结构化输出"},
	},
	{
		category:          regression.FailureKnowledgeRecall,
		inferencePriority: 6,
		exactMetricNames:  []string{"knowledge_recall", "llm_rubric_knowledge_recall", "llm_hallucinations"},
		metricNameSignals: []string{"knowledge", "recall", "retrieval", "知识", "召回", "检索"},
		evidenceSignals: []string{"knowledge", "recall", "retrieval", "missing source", "grounding",
			"知识", "召回", "检索", "缺少来源", "事实依据"},
	},
	{
		category:          regression.FailureFinalResponseMismatch,
		inferencePriority: 7,
		exactMetricNames:  []string{"task_success"},
		metricNameSignals: []string{"final_response", "rouge"},
		evidenceSignals: []string{"answer differs", "incorrect answer", "wrong answer", "response mismatch",
			"expected answer", "reference answer", "答案不一致", "回答错误"},
	},
}

var metricNameTaxonomy = func() []taxonomyEntry {
	entries := append([]taxonomyEntry(nil), failureTaxonomy...)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].inferencePriority < entries[j].inferencePriority
	})
	return entries
}()

func metricCategory(metric regression.MetricResult) regression.FailureCategory {
	name := strings.ToLower(strings.TrimSpace(metric.Name))
	signal := metricSignal(metric)
	if category := standardMetricCategory(name, signal); category != regression.FailureUnknown {
		return category
	}
	if category := inferredMetricCategory(name); category != regression.FailureUnknown {
		return category
	}
	return evidenceSignalCategory(signal)
}

func standardMetricCategory(name, signal string) regression.FailureCategory {
	switch name {
	case "tool_trajectory_avg_score":
		return toolTrajectoryCategory(signal)
	case "final_response_avg_score":
		return finalResponseCategory(signal)
	case "llm_final_response", "llm_rubric_critic", "llm_rubric_reference_critic", "llm_rubric_response":
		return rubricCategory(signal)
	}
	for _, entry := range failureTaxonomy {
		if containsExact(entry.exactMetricNames, name) {
			return entry.category
		}
	}
	return regression.FailureUnknown
}

func inferredMetricCategory(name string) regression.FailureCategory {
	// More specific tool categories must win over the broad tool-selection
	// signal even though safety keeps the highest global precedence.
	for _, entry := range metricNameTaxonomy {
		if containsAny(name, entry.metricNameSignals...) || containsAnyGroup(name, entry.metricNameAll) {
			return entry.category
		}
	}
	return regression.FailureUnknown
}

func toolTrajectoryCategory(signal string) regression.FailureCategory {
	switch {
	case taxonomyEvidenceMatches(regression.FailureToolArgument, signal):
		return regression.FailureToolArgument
	case taxonomyEvidenceMatches(regression.FailureToolResultHandling, signal):
		return regression.FailureToolResultHandling
	default:
		return regression.FailureToolSelection
	}
}

func finalResponseCategory(signal string) regression.FailureCategory {
	if taxonomyEvidenceMatches(regression.FailureFormat, signal) {
		return regression.FailureFormat
	}
	return regression.FailureFinalResponseMismatch
}

func rubricCategory(signal string) regression.FailureCategory {
	if category := evidenceSignalCategory(signal); category != regression.FailureUnknown {
		return category
	}
	return regression.FailureFinalResponseMismatch
}

func evidenceSignalCategory(signal string) regression.FailureCategory {
	for _, entry := range failureTaxonomy {
		if containsAny(signal, entry.evidenceSignals...) {
			return entry.category
		}
	}
	return regression.FailureUnknown
}

func toolExecutionFailure(signal string, tools []regression.ToolObservation) bool {
	normalized := strings.ToLower(strings.TrimSpace(signal))
	if normalized == "" || len(tools) == 0 {
		return false
	}
	if containsAny(normalized, "tool", "function call", "function-call", "工具", "函数调用") {
		return true
	}
	for _, tool := range tools {
		name := strings.ToLower(strings.TrimSpace(tool.Name))
		if len(name) >= 3 && strings.Contains(normalized, name) {
			return true
		}
	}
	return false
}

func metricSignal(metric regression.MetricResult) string {
	var builder strings.Builder
	builder.WriteString(strings.ToLower(metric.Reason))
	for _, rubric := range metric.Rubrics {
		builder.WriteString(" ")
		builder.WriteString(strings.ToLower(rubric.ID))
		builder.WriteString(" ")
		builder.WriteString(strings.ToLower(rubric.Reason))
	}
	return builder.String()
}

func firstRubricReason(metric regression.MetricResult) string {
	for _, rubric := range metric.Rubrics {
		if reason := strings.TrimSpace(rubric.Reason); reason != "" {
			return reason
		}
	}
	return ""
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func containsAnyGroup(value string, groups [][]string) bool {
	for _, group := range groups {
		matched := true
		for _, candidate := range group {
			if !strings.Contains(value, candidate) {
				matched = false
				break
			}
		}
		if matched && len(group) > 0 {
			return true
		}
	}
	return false
}

func containsExact(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func taxonomyFor(category regression.FailureCategory) taxonomyEntry {
	for _, entry := range failureTaxonomy {
		if entry.category == category {
			return entry
		}
	}
	return taxonomyEntry{}
}

func taxonomyEvidenceMatches(category regression.FailureCategory, signal string) bool {
	return containsAny(signal, taxonomyFor(category).evidenceSignals...)
}

func categoryPriority(category regression.FailureCategory) int {
	for priority, entry := range failureTaxonomy {
		if entry.category == category {
			return priority
		}
	}
	return len(failureTaxonomy)
}

func attributed(
	evalSetID string,
	caseID string,
	category regression.FailureCategory,
	reason string,
	source string,
	path string,
	evidence string,
) *regression.AttributionResult {
	return &regression.AttributionResult{
		EvalSetID: evalSetID,
		CaseID:    caseID,
		Category:  category,
		Reason:    reason,
		Evidence:  []regression.Evidence{{Source: source, Path: path, Reason: evidence}},
	}
}
