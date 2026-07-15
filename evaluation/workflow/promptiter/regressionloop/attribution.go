//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// AttributeFailures classifies failed metrics in an evaluation result.
func AttributeFailures(result *promptiterengine.EvaluationResult) []CaseAttribution {
	return AttributeFailuresWithHints(result, nil)
}

// AttributeFailuresWithHints classifies failed metrics using configured metric
// hints before falling back to deterministic metric/reason/trace rules.
func AttributeFailuresWithHints(
	result *promptiterengine.EvaluationResult,
	hints map[string]FailureCategory,
) []CaseAttribution {
	return AttributeFailuresWithMetricDefinitions(result, hints, nil)
}

// AttributeFailuresWithMetricDefinitions classifies failures using structured
// actual/expected evidence first, then configured hints, then metric definition
// signals as weaker fallback evidence.
func AttributeFailuresWithMetricDefinitions(
	result *promptiterengine.EvaluationResult,
	hints map[string]FailureCategory,
	metrics []MetricDefinition,
) []CaseAttribution {
	return AttributeFailuresWithOptions(context.Background(), result, AttributionOptions{
		Hints:   hints,
		Metrics: metrics,
	})
}

// AttributionOptions controls failure attribution. Judge is optional and is
// only used when deterministic attribution has low confidence or no category.
type AttributionOptions struct {
	Hints   map[string]FailureCategory
	Metrics []MetricDefinition
	Judge   AttributionJudge
}

// AttributionJudge is an optional fallback for ambiguous failures. It is
// intentionally injectable so fake/deterministic tests do not need API keys.
type AttributionJudge interface {
	ClassifyFailure(ctx context.Context, request AttributionJudgeRequest) (AttributionJudgeResult, error)
}

// AttributionJudgeRequest carries compact evidence for a fallback classifier.
type AttributionJudgeRequest struct {
	EvalSetID           string            `json:"evalSetId"`
	EvalCaseID          string            `json:"evalCaseId"`
	MetricName          string            `json:"metricName"`
	Reason              string            `json:"reason"`
	Evidence            []string          `json:"evidence,omitempty"`
	CandidateCategories []FailureCategory `json:"candidateCategories,omitempty"`
}

// AttributionJudgeResult is the normalized result returned by a fallback judge.
type AttributionJudgeResult struct {
	Category    FailureCategory   `json:"category"`
	Confidence  float64           `json:"confidence,omitempty"`
	Reason      string            `json:"reason,omitempty"`
	Evidence    []string          `json:"evidence,omitempty"`
	Secondaries []FailureCategory `json:"secondaryCategories,omitempty"`
}

// AttributeFailuresWithOptions classifies failed metrics using structured
// evidence, configured hints, metric definitions, deterministic rules, and an
// optional fallback judge for ambiguous low-confidence cases.
func AttributeFailuresWithOptions(
	ctx context.Context,
	result *promptiterengine.EvaluationResult,
	opts AttributionOptions,
) []CaseAttribution {
	if result == nil {
		return nil
	}
	hints := normalizeAttributionHints(opts.Hints)
	metricSignals := MetricDefinitionHints(opts.Metrics)
	attributions := make([]CaseAttribution, 0)
	for _, evalSet := range result.EvalSets {
		for _, evalCase := range evalSet.Cases {
			if evalCase.Trace != nil && evalCase.Trace.Status == atrace.TraceStatusFailed {
				attributions = append(attributions, CaseAttribution{
					EvalSetID:  evalSet.EvalSetID,
					EvalCaseID: evalCase.EvalCaseID,
					MetricName: "inference",
					Category:   FailureInferenceError,
					Severity:   string(promptiter.LossSeverityP0),
					Method:     "deterministic_rules",
					Confidence: 0.95,
					Reason:     "execution trace failed before evaluation completed",
					Evidence:   traceEvidence(evalCase.Trace),
				})
			}
			for _, metric := range evalCase.Metrics {
				if metric.Status != status.EvalStatusFailed {
					continue
				}
				reason := strings.TrimSpace(metric.Reason)
				if reason == "" {
					reason = fmt.Sprintf("metric %q failed without a detailed reason", metric.MetricName)
				}
				actualInvocation := metric.ActualInvocation
				expectedInvocation := metric.ExpectedInvocation
				if actualInvocation == nil && expectedInvocation == nil {
					actualInvocation = evalCase.ActualInvocation
					expectedInvocation = evalCase.ExpectedInvocation
				}
				category, confidence, method := classifyFailure(
					metric.MetricName,
					reason,
					evalCase.Trace,
					actualInvocation,
					expectedInvocation,
					hints,
					metricSignals,
				)
				evidence := buildEvidence(metric, evalCase.Trace)
				evidence = append(evidence, structuredEvidence(actualInvocation, expectedInvocation)...)
				secondaryCategories := secondaryFailureCategories(
					category,
					metric.MetricName,
					reason,
					evalCase.Trace,
					actualInvocation,
					expectedInvocation,
					hints,
					metricSignals,
				)
				evidence = append(evidence, attributionConflictEvidence(
					category,
					method,
					metric.MetricName,
					hints,
					metricSignals,
				)...)
				if opts.Judge != nil && shouldUseAttributionJudge(category, confidence, method) {
					judgeResult, err := opts.Judge.ClassifyFailure(ctx, AttributionJudgeRequest{
						EvalSetID:           evalSet.EvalSetID,
						EvalCaseID:          evalCase.EvalCaseID,
						MetricName:          metric.MetricName,
						Reason:              reason,
						Evidence:            append([]string(nil), evidence...),
						CandidateCategories: append([]FailureCategory{category}, secondaryCategories...),
					})
					if err != nil {
						evidence = append(evidence, "judge_fallback_error="+trimForEvidence(err.Error()))
					} else if normalized := normalizeFailureCategory(judgeResult.Category); normalized != "" &&
						normalized != FailureUnknown && knownFailureCategory(normalized) {
						secondaryCategories = append([]FailureCategory{category}, secondaryCategories...)
						category = normalized
						confidence = normalizeConfidence(judgeResult.Confidence, 0.7)
						method = "judge_fallback"
						if strings.TrimSpace(judgeResult.Reason) != "" {
							evidence = append(evidence, "judge_reason="+trimForEvidence(judgeResult.Reason))
						}
						for _, item := range judgeResult.Evidence {
							if strings.TrimSpace(item) != "" {
								evidence = append(evidence, "judge_evidence="+trimForEvidence(item))
							}
						}
						secondaryCategories = append(secondaryCategories, normalizeFailureCategories(judgeResult.Secondaries)...)
						secondaryCategories = dedupeCategoriesExcluding(category, secondaryCategories...)
					}
				}
				attributions = append(attributions, CaseAttribution{
					EvalSetID:           evalSet.EvalSetID,
					EvalCaseID:          evalCase.EvalCaseID,
					MetricName:          metric.MetricName,
					Category:            category,
					SecondaryCategories: secondaryCategories,
					Severity:            severityFor(category),
					Method:              method,
					Confidence:          confidence,
					Reason:              reason,
					Evidence:            uniquePreserveStrings(evidence),
				})
			}
		}
	}
	sort.SliceStable(attributions, func(i, j int) bool {
		if attributions[i].EvalSetID != attributions[j].EvalSetID {
			return attributions[i].EvalSetID < attributions[j].EvalSetID
		}
		if attributions[i].EvalCaseID != attributions[j].EvalCaseID {
			return attributions[i].EvalCaseID < attributions[j].EvalCaseID
		}
		return attributions[i].MetricName < attributions[j].MetricName
	})
	return attributions
}

// AttributionHints merges metrics.json hints and promptiter.json attribution
// hints. Config hints win so callers can override shared metric defaults.
func AttributionHints(cfg Config, metrics []MetricDefinition) map[string]FailureCategory {
	hints := make(map[string]FailureCategory)
	for _, metric := range metrics {
		category := normalizeFailureCategory(metric.FailureCategory)
		if metric.MetricName == "" || category == "" || !knownFailureCategory(category) {
			continue
		}
		hints[metric.MetricName] = category
	}
	for metricName, category := range cfg.Attribution.MetricCategoryHints {
		category = normalizeFailureCategory(category)
		if strings.TrimSpace(metricName) == "" || category == "" || !knownFailureCategory(category) {
			continue
		}
		hints[strings.TrimSpace(metricName)] = category
	}
	if len(hints) == 0 {
		return nil
	}
	return hints
}

// MetricDefinitionHints derives weak attribution hints from metrics.json
// criterion/evaluator metadata. Reason text and explicit hints can still
// override these inferred categories.
func MetricDefinitionHints(metrics []MetricDefinition) map[string]FailureCategory {
	hints := make(map[string]FailureCategory)
	for _, metric := range metrics {
		if strings.TrimSpace(metric.MetricName) == "" {
			continue
		}
		category := metricDefinitionCategory(metric)
		if category == "" || category == FailureUnknown || !knownFailureCategory(category) {
			continue
		}
		hints[strings.TrimSpace(metric.MetricName)] = category
	}
	if len(hints) == 0 {
		return nil
	}
	return hints
}

// BuildLossHints converts train-set attributions into PromptIter loss hints.
func BuildLossHints(attributions []CaseAttribution) []promptiterengine.LossHint {
	hints := make([]promptiterengine.LossHint, 0, len(attributions))
	for _, attr := range attributions {
		if strings.TrimSpace(attr.MetricName) == "" || strings.TrimSpace(attr.EvalCaseID) == "" {
			continue
		}
		hints = append(hints, promptiterengine.LossHint{
			EvalCaseID: attr.EvalCaseID,
			MetricName: attr.MetricName,
			Severity:   parseSeverity(attr.Severity),
			Reason:     lossHintReason(attr),
		})
	}
	return hints
}

// SummarizeAttributions builds stable attribution counters for reports.
func SummarizeAttributions(attributions []CaseAttribution) AttributionSummary {
	summary := AttributionSummary{
		Total:               len(attributions),
		ByCategory:          make(map[FailureCategory]int),
		BySecondaryCategory: make(map[FailureCategory]int),
		ByMetric:            make(map[string]int),
		ByCase:              make(map[string][]FailureCategory),
	}
	for _, attr := range attributions {
		summary.ByCategory[attr.Category]++
		for _, category := range attr.SecondaryCategories {
			summary.BySecondaryCategory[category]++
		}
		summary.ByMetric[attr.MetricName]++
		key := attr.EvalSetID + "/" + attr.EvalCaseID
		summary.ByCase[key] = append(summary.ByCase[key], attr.Category)
	}
	if len(summary.BySecondaryCategory) == 0 {
		summary.BySecondaryCategory = nil
	}
	return summary
}

func classifyFailure(
	metricName string,
	reason string,
	trace *atrace.Trace,
	actualInvocation *evalset.Invocation,
	expectedInvocation *evalset.Invocation,
	hints map[string]FailureCategory,
	metricSignals map[string]FailureCategory,
) (FailureCategory, float64, string) {
	configuredCategory := FailureUnknown
	if category, ok := hints[strings.TrimSpace(metricName)]; ok && knownFailureCategory(category) {
		configuredCategory = category
	}
	weakCategory := FailureUnknown
	if category, ok := metricSignals[strings.TrimSpace(metricName)]; ok && knownFailureCategory(category) {
		weakCategory = category
	}
	metricCategory, metricConfidence := metricCategoryHint(metricName)
	if category, confidence, ok := classifyStructuredFailure(
		metricName,
		reason,
		trace,
		actualInvocation,
		expectedInvocation,
		firstKnownCategory(configuredCategory, weakCategory, metricCategory),
	); ok {
		return category, confidence, "structured_diff"
	}
	if traceHasError(trace) && configuredCategory == FailureUnknown &&
		metricCategory == FailureUnknown && weakCategory == FailureUnknown {
		return FailureInferenceError, 0.9, "deterministic_rules"
	}
	if configuredCategory != FailureUnknown {
		return configuredCategory, 0.95, "configured_hint"
	}
	if metricCategory != FailureUnknown && strings.TrimSpace(reason) == "" {
		return metricCategory, metricConfidence, "deterministic_rules"
	}
	if genericFailureReason(reason) {
		if metricCategory != FailureUnknown {
			return metricCategory, metricConfidence, "deterministic_rules"
		}
		if weakCategory != FailureUnknown {
			return weakCategory, 0.72, "metric_definition_hint"
		}
	}
	text := strings.ToLower(metricName + " " + reason + " " + traceErrorText(trace))
	switch {
	case containsAny(text, "timeout", "panic", "exception", "inference failed", "context canceled", "deadline exceeded", "崩溃", "超时"):
		return FailureInferenceError, 0.9, "deterministic_rules"
	case containsAny(text,
		"tool argument", "tool parameter", "argument mismatch", "parameter mismatch",
		"invalid args", "invalid argument", "bad argument", "wrong argument",
		"missing argument", "missing parameter", "tool input", "function arguments",
		"工具参数", "参数错误", "参数缺失", "参数",
	):
		return FailureToolArgumentError, 0.85, "deterministic_rules"
	case containsAny(text,
		"missing tool", "unexpected tool", "wrong tool", "tool trajectory",
		"tool_trajectory", "tool call", "function call", "api call",
		"called the wrong", "did not call", "failed to call", "did not invoke",
		"no tool call", "wrong function", "tool name", "tool selection",
		"工具调用", "未调用", "没有调用", "错误工具", "调用了错误",
	):
		return FailureToolCallError, 0.85, "deterministic_rules"
	case containsAny(text,
		"json", "xml", "format", "parse", "markdown", "schema",
		"structured output", "serialization", "deserialize", "invalid output",
		"missing field", "field missing", "required field", "字段缺失",
		"缺少字段", "结构化字段", "格式",
	):
		return FailureFormatError, 0.8, "deterministic_rules"
	case containsAny(text,
		"route", "router", "handoff", "sub-agent", "subagent", "delegate",
		"agent selection", "selected the wrong agent", "wrong agent",
		"wrong subagent", "wrong sub-agent", "路由", "子代理", "代理选择",
		"选择错误", "转交错误",
	):
		return FailureRouteError, 0.8, "deterministic_rules"
	case containsAny(text,
		"knowledge", "recall", "hallucination", "grounding", "fact",
		"policy", "citation", "source", "retrieve", "retrieval",
		"missing citation", "insufficient source", "insufficient sources",
		"unsupported fact", "factually incorrect", "事实错误", "引用来源不足",
		"来源不足", "检索不足", "事实", "召回", "幻觉",
	):
		return FailureKnowledgeRecallGap, 0.75, "deterministic_rules"
	case containsAny(text,
		"rouge", "final response", "final_response", "answer mismatch",
		"response mismatch", "expected", "actual", "not match",
		"does not match", "mismatch", "incorrect answer", "wrong answer",
		"回复", "答案",
	):
		return FailureFinalResponseMismatch, 0.7, "deterministic_rules"
	case containsAny(text, "rubric", "judge", "llm", "criterion", "criteria", "quality"):
		return FailureRubricFailure, 0.65, "deterministic_rules"
	case containsAny(text, "score below") && metricCategory == FailureUnknown && weakCategory == FailureUnknown:
		return FailureRubricFailure, 0.65, "deterministic_rules"
	case traceHasError(trace):
		return FailureInferenceError, 0.9, "deterministic_rules"
	case metricCategory != FailureUnknown:
		return metricCategory, metricConfidence, "deterministic_rules"
	case weakCategory != FailureUnknown:
		return weakCategory, 0.72, "metric_definition_hint"
	case strings.TrimSpace(reason) != "":
		return FailureRubricFailure, 0.45, "fallback_failed_metric"
	default:
		return FailureUnknown, 0.2, "deterministic_rules"
	}
}

func firstKnownCategory(categories ...FailureCategory) FailureCategory {
	for _, category := range categories {
		if category != "" && category != FailureUnknown && knownFailureCategory(category) {
			return category
		}
	}
	return FailureUnknown
}

func secondaryFailureCategories(
	primary FailureCategory,
	metricName string,
	reason string,
	trace *atrace.Trace,
	actualInvocation *evalset.Invocation,
	expectedInvocation *evalset.Invocation,
	hints map[string]FailureCategory,
	metricSignals map[string]FailureCategory,
) []FailureCategory {
	var categories []FailureCategory
	configuredCategory := FailureUnknown
	if category, ok := hints[strings.TrimSpace(metricName)]; ok && knownFailureCategory(category) {
		configuredCategory = category
	}
	weakCategory := FailureUnknown
	if category, ok := metricSignals[strings.TrimSpace(metricName)]; ok && knownFailureCategory(category) {
		weakCategory = category
	}
	metricCategory, _ := metricCategoryHint(metricName)
	categories = append(categories, structuredFailureCategories(
		metricName,
		reason,
		trace,
		actualInvocation,
		expectedInvocation,
		firstKnownCategory(configuredCategory, weakCategory, metricCategory),
	)...)
	categories = append(categories, configuredCategory, metricCategory, weakCategory)
	categories = append(categories, keywordFailureCategories(metricName, reason, trace)...)
	if traceHasError(trace) {
		categories = append(categories, FailureInferenceError)
	}
	return dedupeCategoriesExcluding(primary, categories...)
}

func attributionConflictEvidence(
	primary FailureCategory,
	method string,
	metricName string,
	hints map[string]FailureCategory,
	metricSignals map[string]FailureCategory,
) []string {
	var evidence []string
	configuredCategory := FailureUnknown
	if category, ok := hints[strings.TrimSpace(metricName)]; ok && knownFailureCategory(category) {
		configuredCategory = category
	}
	if method == "structured_diff" && configuredCategory != FailureUnknown && configuredCategory != primary {
		evidence = append(evidence, fmt.Sprintf("configured_hint=%s overridden_by=structured_diff", configuredCategory))
	}
	if method == "deterministic_rules" {
		if category, ok := metricSignals[strings.TrimSpace(metricName)]; ok &&
			knownFailureCategory(category) && category != FailureUnknown && category != primary {
			evidence = append(evidence, fmt.Sprintf("metric_definition_hint=%s superseded_by_reason_signal", category))
		}
	}
	return evidence
}

func shouldUseAttributionJudge(category FailureCategory, confidence float64, method string) bool {
	if category == FailureUnknown {
		return true
	}
	if method == "fallback_failed_metric" {
		return true
	}
	return category == FailureRubricFailure && confidence <= 0.65
}

func normalizeConfidence(value float64, fallback float64) float64 {
	if value <= 0 {
		return fallback
	}
	if value > 1 {
		return 1
	}
	return value
}

func normalizeFailureCategories(categories []FailureCategory) []FailureCategory {
	out := make([]FailureCategory, 0, len(categories))
	for _, category := range categories {
		category = normalizeFailureCategory(category)
		if category == "" || category == FailureUnknown || !knownFailureCategory(category) {
			continue
		}
		out = append(out, category)
	}
	return out
}

func dedupeCategoriesExcluding(primary FailureCategory, categories ...FailureCategory) []FailureCategory {
	seen := make(map[FailureCategory]struct{}, len(categories))
	out := make([]FailureCategory, 0, len(categories))
	for _, category := range categories {
		category = normalizeFailureCategory(category)
		if category == "" || category == FailureUnknown || category == primary || !knownFailureCategory(category) {
			continue
		}
		if _, ok := seen[category]; ok {
			continue
		}
		seen[category] = struct{}{}
		out = append(out, category)
	}
	return out
}

func uniquePreserveStrings(values []string) []string {
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
	return out
}

func genericFailureReason(reason string) bool {
	text := strings.ToLower(strings.TrimSpace(reason))
	if text == "" {
		return false
	}
	return containsAny(text,
		"failed",
		"failure",
		"score below",
		"below threshold",
		"threshold not met",
		"score too low",
		"low score",
		"did not pass",
		"not pass",
		"不通过",
		"失败",
		"低于阈值",
	)
}

func metricDefinitionCategory(metric MetricDefinition) FailureCategory {
	text := strings.ToLower(strings.TrimSpace(metric.EvaluatorName))
	for key := range metric.Criterion {
		text += " " + strings.ToLower(strings.TrimSpace(key))
		text += " " + metricCriterionText(metric.Criterion[key])
	}
	switch {
	case containsAny(text, "finalresponse", "final_response", "final response", "exact_match", "rouge", "answer", "response_match", "semantic_similarity"):
		return FailureFinalResponseMismatch
	case containsAny(text,
		"tooltrajectory", "tool_trajectory", "tool trajectory", "toolcall", "tool_call", "toolcalls",
		"functioncall", "function_call", "function call", "tool name", "tool_name",
	):
		return FailureToolCallError
	case containsAny(text, "tool arguments", "tool_arguments", "tool args", "tool_args", "function arguments", "function_arguments", "parameters"):
		return FailureToolArgumentError
	case containsAny(text, "json", "xml", "schema", "json_schema", "format", "structured", "structured output"):
		return FailureFormatError
	case containsAny(text, "route", "router", "routerdecision", "router decision", "handoff", "agentselection", "agent selection", "subagent", "sub_agent"):
		return FailureRouteError
	case containsAny(text, "knowledge", "recall", "grounding", "retrieval", "retrieve", "citation", "source", "sources", "fact", "factual"):
		return FailureKnowledgeRecallGap
	case containsAny(text, "rubric", "judge", "llm", "criterion"):
		return FailureRubricFailure
	default:
		return FailureUnknown
	}
}

func metricCriterionText(value json.RawMessage) string {
	if len(value) == 0 {
		return ""
	}
	text := strings.ToLower(string(value))
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return text
	}
	return text + " " + rawJSONText(decoded)
}

func rawJSONText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.ToLower(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, rawJSONText(item))
		}
		return strings.Join(parts, " ")
	case map[string]any:
		parts := make([]string, 0, len(typed)*2)
		for key, item := range typed {
			parts = append(parts, strings.ToLower(key), rawJSONText(item))
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprint(typed)
	}
}

func keywordFailureCategories(metricName string, reason string, trace *atrace.Trace) []FailureCategory {
	text := strings.ToLower(metricName + " " + reason + " " + traceErrorText(trace))
	var categories []FailureCategory
	if containsAny(text, "timeout", "panic", "exception", "inference failed", "context canceled", "deadline exceeded", "崩溃", "超时") {
		categories = append(categories, FailureInferenceError)
	}
	if containsAny(text,
		"tool argument", "tool parameter", "argument mismatch", "parameter mismatch",
		"invalid args", "invalid argument", "bad argument", "wrong argument",
		"missing argument", "missing parameter", "tool input", "function arguments",
		"工具参数", "参数错误", "参数缺失", "参数",
	) {
		categories = append(categories, FailureToolArgumentError)
	}
	if containsAny(text,
		"missing tool", "unexpected tool", "wrong tool", "tool trajectory",
		"tool_trajectory", "tool call", "function call", "api call",
		"called the wrong", "did not call", "failed to call", "did not invoke",
		"no tool call", "wrong function", "tool name", "tool selection",
		"工具调用", "未调用", "没有调用", "错误工具", "调用了错误",
	) {
		categories = append(categories, FailureToolCallError)
	}
	if containsAny(text,
		"json", "xml", "format", "parse", "markdown", "schema",
		"structured output", "serialization", "deserialize", "invalid output",
		"missing field", "field missing", "required field", "字段缺失",
		"缺少字段", "结构化字段", "格式",
	) {
		categories = append(categories, FailureFormatError)
	}
	if containsAny(text,
		"route", "router", "handoff", "sub-agent", "subagent", "delegate",
		"agent selection", "selected the wrong agent", "wrong agent",
		"wrong subagent", "wrong sub-agent", "路由", "子代理", "代理选择",
		"选择错误", "转交错误",
	) {
		categories = append(categories, FailureRouteError)
	}
	if containsAny(text,
		"knowledge", "recall", "hallucination", "grounding", "fact",
		"policy", "citation", "source", "retrieve", "retrieval",
		"missing citation", "insufficient source", "insufficient sources",
		"unsupported fact", "factually incorrect", "事实错误", "引用来源不足",
		"来源不足", "检索不足", "事实", "召回", "幻觉",
	) {
		categories = append(categories, FailureKnowledgeRecallGap)
	}
	if containsAny(text,
		"rouge", "final response", "final_response", "answer mismatch",
		"response mismatch", "expected", "actual", "not match",
		"does not match", "mismatch", "incorrect answer", "wrong answer",
		"回复", "答案",
	) {
		categories = append(categories, FailureFinalResponseMismatch)
	}
	if containsAny(text, "rubric", "judge", "llm", "criterion", "criteria", "quality") {
		categories = append(categories, FailureRubricFailure)
	}
	return categories
}

func metricCategoryHint(metricName string) (FailureCategory, float64) {
	name := strings.ToLower(strings.TrimSpace(metricName))
	switch {
	case name == "final_response" || strings.Contains(name, "rouge") || strings.Contains(name, "answer"):
		return FailureFinalResponseMismatch, 0.75
	case strings.Contains(name, "tool_argument") || strings.Contains(name, "tool_args") || strings.Contains(name, "parameter"):
		return FailureToolArgumentError, 0.85
	case strings.Contains(name, "tool_trajectory") || strings.Contains(name, "tool_call"):
		return FailureToolCallError, 0.85
	case strings.Contains(name, "router") || strings.Contains(name, "route"):
		return FailureRouteError, 0.85
	case strings.Contains(name, "json") || strings.Contains(name, "schema") || strings.Contains(name, "format"):
		return FailureFormatError, 0.85
	case strings.Contains(name, "knowledge") || strings.Contains(name, "recall") || strings.Contains(name, "grounding"):
		return FailureKnowledgeRecallGap, 0.8
	case strings.Contains(name, "rubric") || strings.Contains(name, "judge") || strings.Contains(name, "llm"):
		return FailureRubricFailure, 0.7
	default:
		return FailureUnknown, 0.0
	}
}

func traceErrorText(trace *atrace.Trace) string {
	if trace == nil {
		return ""
	}
	var parts []string
	if trace.Status == atrace.TraceStatusFailed {
		parts = append(parts, string(trace.Status))
	}
	for _, step := range trace.Steps {
		if strings.TrimSpace(step.Error) != "" {
			parts = append(parts, step.Error)
		}
	}
	return strings.Join(parts, " ")
}

func severityFor(category FailureCategory) string {
	switch category {
	case FailureInferenceError, FailureRouteError:
		return string(promptiter.LossSeverityP0)
	case FailureToolCallError, FailureToolArgumentError, FailureFormatError:
		return string(promptiter.LossSeverityP1)
	case FailureFinalResponseMismatch, FailureKnowledgeRecallGap:
		return string(promptiter.LossSeverityP2)
	default:
		return string(promptiter.LossSeverityP3)
	}
}

func parseSeverity(value string) promptiter.LossSeverity {
	switch promptiter.LossSeverity(value) {
	case promptiter.LossSeverityP0, promptiter.LossSeverityP1, promptiter.LossSeverityP2, promptiter.LossSeverityP3:
		return promptiter.LossSeverity(value)
	default:
		return promptiter.LossSeverityP2
	}
}

func lossHintReason(attr CaseAttribution) string {
	parts := []string{
		fmt.Sprintf("failure_category=%s", attr.Category),
		"reason=" + attr.Reason,
	}
	if len(attr.Evidence) > 0 {
		parts = append(parts, "evidence="+strings.Join(attr.Evidence, "; "))
	}
	return strings.Join(parts, "\n")
}

func buildEvidence(metric promptiterengine.MetricResult, trace *atrace.Trace) []string {
	evidence := []string{
		fmt.Sprintf("metric=%s status=%s score=%.3f", metric.MetricName, metric.Status, metric.Score),
	}
	if strings.TrimSpace(metric.Reason) != "" {
		evidence = append(evidence, "metric_reason="+trimForEvidence(metric.Reason))
	}
	evidence = append(evidence, traceEvidence(trace)...)
	return evidence
}

func traceEvidence(trace *atrace.Trace) []string {
	if trace == nil {
		return nil
	}
	evidence := []string{fmt.Sprintf("trace_status=%s", trace.Status)}
	for _, step := range trace.Steps {
		if strings.TrimSpace(step.Error) != "" {
			evidence = append(evidence, fmt.Sprintf("step_error[%s]=%s", step.StepID, trimForEvidence(step.Error)))
		}
	}
	if text := finalTraceText(trace); text != "" {
		evidence = append(evidence, "final_output="+trimForEvidence(text))
	}
	return evidence
}

func finalTraceText(trace *atrace.Trace) string {
	if trace == nil {
		return ""
	}
	for i := len(trace.Steps) - 1; i >= 0; i-- {
		if trace.Steps[i].Output != nil && strings.TrimSpace(trace.Steps[i].Output.Text) != "" {
			return trace.Steps[i].Output.Text
		}
	}
	return ""
}

func traceHasError(trace *atrace.Trace) bool {
	if trace == nil {
		return false
	}
	if trace.Status == atrace.TraceStatusFailed {
		return true
	}
	for _, step := range trace.Steps {
		if strings.TrimSpace(step.Error) != "" {
			return true
		}
	}
	return false
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func trimForEvidence(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= 180 {
		return text
	}
	return string(runes[:177]) + "..."
}

func normalizeAttributionHints(hints map[string]FailureCategory) map[string]FailureCategory {
	if len(hints) == 0 {
		return nil
	}
	out := make(map[string]FailureCategory, len(hints))
	for metricName, category := range hints {
		metricName = strings.TrimSpace(metricName)
		category = normalizeFailureCategory(category)
		if metricName == "" || category == "" || !knownFailureCategory(category) {
			continue
		}
		out[metricName] = category
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeFailureCategory(category FailureCategory) FailureCategory {
	return FailureCategory(strings.TrimSpace(strings.ToLower(string(category))))
}

func knownFailureCategory(category FailureCategory) bool {
	switch category {
	case FailureFinalResponseMismatch,
		FailureToolCallError,
		FailureToolArgumentError,
		FailureRouteError,
		FailureFormatError,
		FailureKnowledgeRecallGap,
		FailureRubricFailure,
		FailureInferenceError,
		FailureUnknown:
		return true
	default:
		return false
	}
}
