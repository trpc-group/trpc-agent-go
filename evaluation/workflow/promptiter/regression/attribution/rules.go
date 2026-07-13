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

type classifiedMetric struct {
	metric   regression.MetricResult
	category regression.FailureCategory
}

func metricCategory(metric regression.MetricResult) regression.FailureCategory {
	name := strings.ToLower(strings.TrimSpace(metric.Name))
	if category := standardMetricCategory(name, metricSignal(metric)); category != regression.FailureUnknown {
		return category
	}
	return inferredMetricCategory(name)
}

func standardMetricCategory(name, signal string) regression.FailureCategory {
	switch name {
	case "safety":
		return regression.FailureSafetyPolicy
	case "tool_selection":
		return regression.FailureToolSelection
	case "tool_arguments":
		return regression.FailureToolArgument
	case "route":
		return regression.FailureRoute
	case "format":
		return regression.FailureFormat
	case "knowledge_recall", "llm_rubric_knowledge_recall", "llm_hallucinations":
		return regression.FailureKnowledgeRecall
	case "task_success":
		return regression.FailureFinalResponseMismatch
	case "tool_trajectory_avg_score":
		return toolTrajectoryCategory(signal)
	case "final_response_avg_score":
		return finalResponseCategory(signal)
	case "llm_final_response", "llm_rubric_critic", "llm_rubric_reference_critic", "llm_rubric_response":
		return rubricCategory(signal)
	default:
		return regression.FailureUnknown
	}
}

func inferredMetricCategory(name string) regression.FailureCategory {
	switch {
	case containsAny(name, "safety", "privacy", "安全", "隐私"):
		return regression.FailureSafetyPolicy
	case (strings.Contains(name, "tool") && strings.Contains(name, "argument")) ||
		containsAny(name, "工具参数", "参数错误"):
		return regression.FailureToolArgument
	case (strings.Contains(name, "tool") && strings.Contains(name, "result")) ||
		containsAny(name, "工具结果", "结果处理"):
		return regression.FailureToolResultHandling
	case strings.Contains(name, "tool") || strings.Contains(name, "工具"):
		return regression.FailureToolSelection
	case strings.Contains(name, "route") || strings.Contains(name, "router") || strings.Contains(name, "路由"):
		return regression.FailureRoute
	case strings.Contains(name, "format") || strings.Contains(name, "structured") || strings.Contains(name, "格式"):
		return regression.FailureFormat
	case strings.Contains(name, "knowledge") || strings.Contains(name, "recall") ||
		strings.Contains(name, "retrieval") || containsAny(name, "知识", "召回", "检索"):
		return regression.FailureKnowledgeRecall
	case strings.Contains(name, "final_response") || strings.Contains(name, "rouge"):
		return regression.FailureFinalResponseMismatch
	default:
		return regression.FailureUnknown
	}
}

func toolTrajectoryCategory(signal string) regression.FailureCategory {
	switch {
	case strings.Contains(signal, "arguments mismatch") || strings.Contains(signal, "parameter") ||
		containsAny(signal, "工具参数", "参数错误", "参数缺失"):
		return regression.FailureToolArgument
	case strings.Contains(signal, "result mismatch") || strings.Contains(signal, "tool result") ||
		containsAny(signal, "工具结果", "结果处理"):
		return regression.FailureToolResultHandling
	default:
		return regression.FailureToolSelection
	}
}

func finalResponseCategory(signal string) regression.FailureCategory {
	if containsAny(signal, "json", "xml", "schema", "format", "structured output", "格式", "结构化输出") {
		return regression.FailureFormat
	}
	return regression.FailureFinalResponseMismatch
}

func rubricCategory(signal string) regression.FailureCategory {
	switch {
	case containsAny(signal, "safety", "unsafe", "privacy", "private data", "policy violation",
		"安全", "不安全", "隐私", "泄露", "违规", "有害"):
		return regression.FailureSafetyPolicy
	case containsAny(signal, "tool argument", "tool parameter", "arguments mismatch",
		"工具参数", "参数错误", "参数缺失"):
		return regression.FailureToolArgument
	case containsAny(signal, "tool selection", "wrong tool", "tool call", "工具选择", "错误工具", "工具调用"):
		return regression.FailureToolSelection
	case containsAny(signal, "route", "router", "transfer", "sub-agent", "路由", "子代理", "转交"):
		return regression.FailureRoute
	case containsAny(signal, "json", "xml", "schema", "format", "structured output", "格式", "结构化输出"):
		return regression.FailureFormat
	case containsAny(signal, "knowledge", "recall", "retrieval", "missing source", "grounding",
		"知识", "召回", "检索", "缺少来源", "事实依据"):
		return regression.FailureKnowledgeRecall
	default:
		return regression.FailureFinalResponseMismatch
	}
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

func categoryPriority(category regression.FailureCategory) int {
	switch category {
	case regression.FailureSafetyPolicy:
		return 0
	case regression.FailureToolSelection:
		return 1
	case regression.FailureToolArgument:
		return 2
	case regression.FailureToolResultHandling:
		return 3
	case regression.FailureRoute:
		return 4
	case regression.FailureFormat:
		return 5
	case regression.FailureKnowledgeRecall:
		return 6
	case regression.FailureFinalResponseMismatch:
		return 7
	default:
		return 8
	}
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
