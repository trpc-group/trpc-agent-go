//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

// AttributeFailure classifies a failed case using structured signals first and
// rubric text only as a fallback. Every failed case receives at least one reason.
func AttributeFailure(result CaseResult) []Attribution {
	if result.Passed {
		return nil
	}
	attributions := make(map[FailureCategory]Attribution)
	add := func(category FailureCategory, confidence float64, evidence string, signals ...string) {
		candidate := Attribution{
			Category:   category,
			Confidence: confidence,
			Evidence:   evidence,
			Signals:    append([]string(nil), signals...),
		}
		current, ok := attributions[category]
		if !ok || candidate.Confidence > current.Confidence {
			attributions[category] = candidate
		}
	}

	if result.Error != "" {
		if len(result.ExpectedToolTrajectory) > 0 || len(result.ToolTrajectory) > 0 {
			add(FailureToolCallError, 0.99, "inference/tool execution failed: "+result.Error, "error", "trace")
		} else {
			add(FailureFinalResponseMismatch, 0.8, "inference failed before a valid final response: "+result.Error, "error")
		}
	}
	if result.ExpectedRoute != "" && result.ExpectedRoute != result.Route {
		add(
			FailureRouteError,
			0.99,
			fmt.Sprintf("expected route %q, got %q", result.ExpectedRoute, result.Route),
			"route",
			"trace",
		)
	}
	attributeTrace(result, add)
	attributeTools(result.ExpectedToolTrajectory, result.ToolTrajectory, add)
	if result.ResponseFormat != "" && !result.StructuredValid {
		add(
			FailureFormatError,
			0.99,
			fmt.Sprintf("final response violates required %s format", result.ResponseFormat),
			"structured_output",
			"final_response",
		)
	}
	if knowledgeSignalsInsufficient(result) {
		add(
			FailureKnowledgeRetrievalInsufficient,
			0.98,
			fmt.Sprintf(
				"retrieved %d documents and %d/%d required facts",
				result.RetrievedDocuments,
				factHits(result.RequiredFacts, result.RetrievedFacts),
				len(result.RequiredFacts),
			),
			"retrieval",
			"knowledge_recall",
		)
	}
	for _, metricResult := range result.MetricResults {
		if metricResult.Passed {
			continue
		}
		switch metricResult.MetricName {
		case metricFinalResponse, metricLLMRubric:
			add(
				FailureFinalResponseMismatch,
				0.9,
				defaultReason(metricResult.Reason, "final response metric failed"),
				metricResult.MetricName,
				"final_response",
			)
		case metricRoute:
			add(FailureRouteError, 0.95, metricResult.Reason, metricResult.MetricName)
		case metricStructuredOutput:
			add(FailureFormatError, 0.95, metricResult.Reason, metricResult.MetricName)
		case metricKnowledgeRecall:
			add(FailureKnowledgeRetrievalInsufficient, 0.95, metricResult.Reason, metricResult.MetricName)
		}
	}
	rubricFailed := false
	for _, metricResult := range result.MetricResults {
		if metricResult.MetricName == metricLLMRubric && !metricResult.Passed {
			rubricFailed = true
			break
		}
	}
	if rubricFailed {
		attributeRubricKeywords(result.RubricReason, add)
	}
	if len(attributions) == 0 {
		add(
			FailureFinalResponseMismatch,
			0.5,
			"case failed evaluation but no more specific structured signal was available",
			"fallback",
		)
	}

	order := map[FailureCategory]int{
		FailureRouteError:                     0,
		FailureToolCallError:                  1,
		FailureToolParameterError:             2,
		FailureFormatError:                    3,
		FailureKnowledgeRetrievalInsufficient: 4,
		FailureFinalResponseMismatch:          5,
	}
	resultList := make([]Attribution, 0, len(attributions))
	for _, attribution := range attributions {
		resultList = append(resultList, attribution)
	}
	sort.SliceStable(resultList, func(i, j int) bool {
		if resultList[i].Confidence != resultList[j].Confidence {
			return resultList[i].Confidence > resultList[j].Confidence
		}
		left, leftOK := order[resultList[i].Category]
		right, rightOK := order[resultList[j].Category]
		if !leftOK {
			left = len(order)
		}
		if !rightOK {
			right = len(order)
		}
		if left != right {
			return left < right
		}
		return resultList[i].Category < resultList[j].Category
	})
	return resultList
}

func attributeTools(
	expected []*evalset.Tool,
	actual []*evalset.Tool,
	add func(FailureCategory, float64, string, ...string),
) {
	if len(expected) != len(actual) {
		add(
			FailureToolCallError,
			0.99,
			fmt.Sprintf("expected %d tool calls, got %d", len(expected), len(actual)),
			"tool_trajectory",
		)
	}
	for i := 0; i < len(expected) && i < len(actual); i++ {
		if expected[i] == nil || actual[i] == nil {
			add(FailureToolCallError, 0.98, fmt.Sprintf("nil tool call at position %d", i), "tool_trajectory")
			continue
		}
		if expected[i].Name != actual[i].Name {
			add(
				FailureToolCallError,
				0.99,
				fmt.Sprintf("expected tool %q, got %q at position %d", expected[i].Name, actual[i].Name, i),
				"tool_trajectory",
				"tool_name",
			)
			continue
		}
		argumentsMatch := expected[i].Arguments == nil || canonicalEqual(expected[i].Arguments, actual[i].Arguments)
		if expected[i].Arguments != nil && !argumentsMatch {
			add(
				FailureToolParameterError,
				0.99,
				fmt.Sprintf("tool %q arguments differ from reference", expected[i].Name),
				"tool_trajectory",
				"tool_arguments",
			)
		}
		// A result mismatch after wrong arguments is downstream evidence of the
		// parameter error, not a second root-cause tool-call error.
		if expected[i].Result != nil && argumentsMatch && !canonicalEqual(expected[i].Result, actual[i].Result) {
			add(
				FailureToolCallError,
				0.9,
				fmt.Sprintf("tool %q result differs from reference", expected[i].Name),
				"tool_trajectory",
				"tool_result",
			)
		}
	}
}

func attributeTrace(
	result CaseResult,
	add func(FailureCategory, float64, string, ...string),
) {
	lastSuccessfulRoute := ""
	for _, step := range result.Trace {
		status := strings.ToLower(step.Status)
		failed := traceStatusIndicatesFailure(status) ||
			strings.Contains(result.Error, "trace step "+step.StepID+" ended with status")
		kind := strings.ToLower(step.Kind)
		if failed {
			evidence := fmt.Sprintf("trace step %q (%s/%s) failed", step.StepID, step.Kind, step.Status)
			if step.Message != "" {
				evidence += ": " + step.Message
			}
			switch {
			case isRouteTraceStep(kind):
				add(FailureRouteError, 0.99, evidence, "trace", "route")
			case isToolCallTraceStep(kind):
				add(FailureToolCallError, 0.99, evidence, "trace", "tool_execution")
			case isRetrievalTraceStep(kind):
				add(FailureKnowledgeRetrievalInsufficient, 0.95, evidence, "trace", "retrieval")
			case strings.Contains(kind, "format"), strings.Contains(kind, "schema"), strings.Contains(kind, "structured"):
				add(FailureFormatError, 0.95, evidence, "trace", "structured_output")
			}
		}
		if !failed && traceStatusSuccessful(step.Status) && isRouteTraceStep(kind) && step.Name != "" {
			lastSuccessfulRoute = step.Name
		}
	}
	if result.ExpectedRoute != "" && lastSuccessfulRoute != "" && lastSuccessfulRoute != result.ExpectedRoute {
		add(
			FailureRouteError,
			0.98,
			fmt.Sprintf("trace selected final route %q instead of %q", lastSuccessfulRoute, result.ExpectedRoute),
			"trace",
			"route",
		)
	}
}

func knowledgeSignalsInsufficient(result CaseResult) bool {
	if len(result.RequiredFacts) > 0 && factHits(result.RequiredFacts, result.RetrievedFacts) < len(result.RequiredFacts) {
		return true
	}
	return result.MinRetrievedDocuments > 0 && result.RetrievedDocuments < result.MinRetrievedDocuments
}

func factHits(expected, actual []string) int {
	actualSet := make(map[string]struct{}, len(actual))
	for _, fact := range actual {
		actualSet[normalizeText(fact)] = struct{}{}
	}
	hits := 0
	for _, fact := range expected {
		if _, ok := actualSet[normalizeText(fact)]; ok {
			hits++
		}
	}
	return hits
}

func attributeRubricKeywords(
	reason string,
	add func(FailureCategory, float64, string, ...string),
) {
	normalized := strings.TrimSpace(strings.ToLower(strings.ReplaceAll(reason, "’", "'")))
	if normalized == "" {
		return
	}
	negativeCategories := make(map[FailureCategory]struct{})
	for _, clause := range splitRubricClauses(normalized) {
		if contrastCategories, handled := rubricContrastCategories(clause); handled {
			for category := range contrastCategories {
				negativeCategories[category] = struct{}{}
			}
			continue
		}
		explicitCategories, suppressedCategories := explicitRubricFailureCategories(clause)
		for category := range explicitCategories {
			negativeCategories[category] = struct{}{}
		}
		allTopics := findRubricTopicSpans(clause, rubricFailureTopics)
		finalTopics := removeContainedRubricSpans(findRubricTermSpans(clause, []string{
			"final answer", "final response", "assistant response", "generated response",
			"answer", "response", "reply", "最终答案", "最终回复", "答案", "回复", "答复", "响应",
		}, FailureFinalResponseMismatch))
		allTopics = append(allTopics, finalTopics...)
		allTopics = filterRubricModifierTopics(clause, removeContainedRubricSpans(allTopics))
		for _, cue := range rubricPolarityCues(clause) {
			if cue.polarity != rubricPolarityNegative {
				continue
			}
			if rubricCueWasResolved(clause, cue) {
				continue
			}
			for _, owner := range nearestRubricTopics(clause, cue, allTopics) {
				_, suppressed := suppressedCategories[owner.category]
				if owner.category != FailureFinalResponseMismatch && !suppressed {
					negativeCategories[owner.category] = struct{}{}
				}
			}
		}
	}
	for _, topic := range rubricFailureTopics {
		if _, ok := negativeCategories[topic.category]; ok {
			add(topic.category, topic.confidence, reason, "rubric")
		}
	}
}

func rubricContrastCategories(clause string) (map[FailureCategory]struct{}, bool) {
	type contrastPattern struct {
		negative string
		positive string
	}
	for _, pattern := range []contrastPattern{
		{negative: "不是", positive: "而是"},
		{negative: "不在", positive: "而在"},
		{negative: " not ", positive: " but "},
		{negative: " not ", positive: ", but "},
	} {
		negativeIndex := strings.Index(clause, pattern.negative)
		if negativeIndex < 0 {
			continue
		}
		positiveOffset := negativeIndex + len(pattern.negative)
		positiveRelative := strings.Index(clause[positiveOffset:], pattern.positive)
		if positiveRelative < 0 {
			continue
		}
		positiveIndex := positiveOffset + positiveRelative
		excluded := rubricCategoriesInText(clause[positiveOffset:positiveIndex])
		included := rubricCategoriesInText(clause[positiveIndex+len(pattern.positive):])
		if len(excluded) > 0 && len(included) > 0 {
			return included, true
		}
	}

	for _, marker := range []string{", not ", ", rather than "} {
		markerIndex := strings.Index(clause, marker)
		if markerIndex < 0 {
			continue
		}
		excludedStart := markerIndex + len(marker)
		tailRelative := strings.Index(clause[excludedStart:], ",")
		if tailRelative < 0 {
			continue
		}
		tailIndex := excludedStart + tailRelative
		included := rubricCategoriesInText(clause[:markerIndex])
		excluded := rubricCategoriesInText(clause[excludedStart:tailIndex])
		if len(included) == 0 || len(excluded) == 0 || !rubricTextHasNegativeCue(clause[tailIndex+1:]) {
			continue
		}
		return included, true
	}
	return nil, false
}

func rubricCategoriesInText(text string) map[FailureCategory]struct{} {
	spans := findRubricTopicSpans(text, rubricFailureTopics)
	spans = filterRubricModifierTopics(text, removeContainedRubricSpans(spans))
	categories := make(map[FailureCategory]struct{}, len(spans))
	for _, span := range spans {
		categories[span.category] = struct{}{}
	}
	return categories
}

func rubricTextHasNegativeCue(text string) bool {
	for _, cue := range rubricPolarityCues(text) {
		if cue.polarity == rubricPolarityNegative && !rubricCueWasResolved(text, cue) {
			return true
		}
	}
	return false
}

func explicitRubricFailureCategories(
	clause string,
) (map[FailureCategory]struct{}, map[FailureCategory]struct{}) {
	negative := make(map[FailureCategory]struct{})
	suppressed := make(map[FailureCategory]struct{})
	if rubricContainsAny(
		clause,
		"no tool call was required", "no tool call required", "tool call was not required", "tool was not required",
		"无需调用工具", "不需要工具调用", "不需要调用工具",
	) {
		suppressed[FailureToolCallError] = struct{}{}
	}
	if rubricContainsAny(clause, "tool input", "tool inputs", "工具输入") &&
		rubricContainsAny(clause, "incorrect", "wrong", "invalid", "missing", "not set", "never set", "不正确", "错误", "无效", "缺失", "未设置") {
		negative[FailureToolParameterError] = struct{}{}
		suppressed[FailureToolCallError] = struct{}{}
	}
	if rubricContainsAny(clause, "output", "输出") &&
		rubricContainsAny(clause, "could not be parsed", "cannot be parsed", "failed to parse", "parse failed", "无法解析", "不能解析", "解析失败") {
		negative[FailureFormatError] = struct{}{}
	}
	if strings.Contains(clause, "普通文本") && strings.Contains(clause, "json") {
		negative[FailureFormatError] = struct{}{}
	}
	return negative, suppressed
}

func rubricContainsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func rubricCueWasResolved(clause string, cue rubricTextSpan) bool {
	if cue.end >= len(clause) {
		return false
	}
	suffix := clause[cue.end:]
	for _, separator := range []string{", but ", ", however ", ", yet ", "，但", "，但是", "，然而", "；但", "；但是"} {
		if index := strings.Index(suffix, separator); index >= 0 {
			suffix = suffix[:index]
		}
	}
	for _, term := range []string{
		"fixed", "resolved", "repaired", "corrected",
		"已经修复", "已修复", "修复了", "已经解决", "已解决", "解决了", "已经纠正", "已纠正", "已补齐",
	} {
		for offset := 0; offset < len(suffix); {
			index := strings.Index(suffix[offset:], term)
			if index < 0 {
				break
			}
			index += offset
			if !rubricResolutionIsNegated(suffix[:index]) {
				return true
			}
			offset = index + len(term)
		}
	}
	return false
}

func rubricResolutionIsNegated(prefix string) bool {
	trimmed := strings.TrimSpace(prefix)
	for _, suffix := range []string{
		"not", "never", "no", "wasn't", "isn't", "hasn't", "hadn't",
		"未", "没有", "没", "不", "并未", "尚未", "从未",
	} {
		if strings.HasSuffix(trimmed, suffix) {
			return true
		}
	}
	return false
}

type rubricTopic struct {
	category   FailureCategory
	confidence float64
	terms      []string
}

var rubricFailureTopics = []rubricTopic{
	{
		category:   FailureRouteError,
		confidence: 0.92,
		terms: []string{
			"route", "routing", "router", "handoff", "transfer", "agent selection",
			"路由", "转交", "转派", "分流", "代理选择", "智能体选择",
		},
	},
	{
		category:   FailureToolParameterError,
		confidence: 0.92,
		terms: []string{
			"tool input", "tool inputs", "input parameter", "input parameters",
			"argument", "arguments", "parameter", "parameters", "payload", "input value",
			"工具输入", "参数", "入参", "实参", "参数值",
		},
	},
	{
		category:   FailureToolCallError,
		confidence: 0.9,
		terms: []string{
			"tool call", "tool invocation", "tool selection", "tool execution", "function call", "tool",
			"工具调用", "工具选择", "工具执行", "函数调用", "工具",
		},
	},
	{
		category:   FailureFormatError,
		confidence: 0.92,
		terms: []string{
			"required field", "required fields", "output format", "output schema",
			"json", "xml", "yaml", "schema", "format", "structured output",
			"必填字段", "格式", "结构化输出", "结构化",
		},
	},
	{
		category:   FailureKnowledgeRetrievalInsufficient,
		confidence: 0.92,
		terms: []string{
			"retrieval", "retrieved", "grounding", "grounded", "knowledge", "context", "evidence",
			"召回", "检索", "知识", "上下文", "依据", "证据",
		},
	},
}

type rubricPolarity int

const (
	rubricPolarityPositive rubricPolarity = 1
	rubricPolarityNegative rubricPolarity = -1
)

type rubricTextSpan struct {
	start    int
	end      int
	category FailureCategory
	polarity rubricPolarity
}

func splitRubricClauses(reason string) []string {
	// Keep commas, colons, and conjunctions in the clause: they often connect
	// a rubric label/predicate ("Route: wrong") or an execution context
	// ("failed while invoking the tool"). Predicate-to-topic assignment below
	// handles contrast and coordination without discarding that relationship.
	parts := strings.FieldsFunc(reason, func(r rune) bool {
		switch r {
		case '.', ';', '!', '?', '\n', '\r', '。', '；', '！', '？':
			return true
		default:
			return false
		}
	})
	clauses := make([]string, 0, len(parts))
	for _, part := range parts {
		if clause := strings.TrimSpace(part); clause != "" {
			clauses = append(clauses, clause)
		}
	}
	return clauses
}

func findRubricTopicSpans(clause string, topics []rubricTopic) []rubricTextSpan {
	var spans []rubricTextSpan
	for _, topic := range topics {
		spans = append(spans, findRubricTermSpans(clause, topic.terms, topic.category)...)
	}
	return spans
}

func findRubricTermSpans(
	text string,
	terms []string,
	category FailureCategory,
) []rubricTextSpan {
	var spans []rubricTextSpan
	for _, term := range terms {
		for offset := 0; offset < len(text); {
			index := strings.Index(text[offset:], term)
			if index < 0 {
				break
			}
			start := offset + index
			end := start + len(term)
			if rubricTermBoundary(text, start, end) {
				spans = append(spans, rubricTextSpan{start: start, end: end, category: category})
			}
			offset = start + len(term)
		}
	}
	return spans
}

func removeContainedRubricSpans(spans []rubricTextSpan) []rubricTextSpan {
	filtered := make([]rubricTextSpan, 0, len(spans))
	for i, candidate := range spans {
		contained := false
		for j, other := range spans {
			if i != j && candidate.category == other.category &&
				other.start <= candidate.start && other.end >= candidate.end &&
				other.end-other.start > candidate.end-candidate.start {
				contained = true
				break
			}
		}
		if !contained {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func filterRubricModifierTopics(clause string, spans []rubricTextSpan) []rubricTextSpan {
	filtered := make([]rubricTextSpan, 0, len(spans))
	for _, candidate := range spans {
		candidateText := clause[candidate.start:candidate.end]
		if candidate.category == FailureToolCallError &&
			(candidateText == "tool" || candidateText == "工具") &&
			rubricToolModifiesParameter(clause, candidate, spans) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

func rubricToolModifiesParameter(
	clause string,
	tool rubricTextSpan,
	spans []rubricTextSpan,
) bool {
	for _, parameter := range spans {
		if parameter.category != FailureToolParameterError {
			continue
		}
		if parameter.start <= tool.start && parameter.end >= tool.end {
			return true
		}
		left, right := tool, parameter
		if left.start > right.start {
			left, right = right, left
		}
		if right.start-left.end > 24 {
			continue
		}
		between := strings.TrimSpace(clause[left.end:right.start])
		switch between {
		case "", "'s", "of", "of the", "for", "for the", "to", "to the", "的", "对应", "对应的", "所属", "所属的":
			return true
		}
	}
	return false
}

func rubricTermBoundary(text string, start, end int) bool {
	if start > 0 && rubricASCIIWordByte(text[start-1]) && rubricASCIIWordByte(text[start]) {
		return false
	}
	if end < len(text) && rubricASCIIWordByte(text[end-1]) && rubricASCIIWordByte(text[end]) {
		return false
	}
	return true
}

func rubricASCIIWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_'
}

var rubricPositiveCueTerms = []string{
	"nothing wrong", "without any issues", "without any errors", "without issues", "without errors",
	"free of issues", "free of errors", "no problems", "no problem", "no issues", "no issue",
	"no errors", "no error", "as expected", "meets requirements", "met requirements",
	"well-formed", "successful", "succeeded", "succeed", "sufficient", "adequate", "correct", "right",
	"valid", "complete", "matched", "matches", "match", "consistent", "worked", "works", "working",
	"available", "present", "called", "invoked", "selected", "routed", "transferred",
	"parsed", "parsing", "parse", "retrieved", "retrieve", "retrieval", "returned", "returning",
	"json", "xml", "yaml", "call", "okay", "ok", "fine", "proper", "accurate",
	"没有任何问题", "不存在任何问题", "未发现任何问题", "没有问题", "不存在问题", "未发现问题",
	"并无问题", "没问题", "无问题", "没有错误", "不存在错误", "未发现错误", "并无错误",
	"无误", "无异常", "符合预期", "满足要求", "正确", "充分", "足够", "完整", "合法",
	"有效", "匹配", "一致", "正常", "成功", "调用", "选择", "解析", "检索", "召回", "返回",
}

var rubricNegativeCueTerms = []string{
	"not fixed", "not resolved", "not repaired", "not corrected",
	"did not select", "didn't select", "did not hit", "didn't hit", "failed to select", "failed to route",
	"never happened", "did not happen", "didn't happen", "was repeated", "repeated",
	"never set", "not set", "unset", "could not be parsed", "cannot be parsed", "failed to parse", "parse failed",
	"missing required fields", "missing required field", "no relevant documents", "no relevant docs", "no hits",
	"irrelevant", "unrelated", "coverage was low", "low coverage", "not json", "non-json", "nonjson",
	"not entirely correct", "not fully correct", "not well-formed", "not successful", "not correct",
	"not right", "not valid", "not sufficient", "not adequate", "not enough", "not complete",
	"not matched", "not consistent", "not called", "no correct", "no documents", "nothing retrieved",
	"did not work", "does not work", "didn't work", "could not parse", "cannot parse", "not found",
	"timed out", "timed-out", "parse failure", "parse error", "instead of",
	"malformed", "unparsable", "unsupported", "unsuccessful", "insufficient", "inadequate",
	"incomplete", "inconsistent", "incorrect", "invalid", "mismatched", "mismatch", "missing", "omitted",
	"failed", "failure", "fail",
	"errors", "error", "issues", "issue", "problems", "problem", "wrong", "flawed", "bad", "broken",
	"problematic", "timeout", "unavailable", "absent", "different", "differs", "differed", "differ", "discrepancies",
	"discrepancy", "zero", "none", "empty", "poor", "weak",
	"未修复", "没有修复", "未解决", "没有解决", "未纠正", "没有纠正",
	"未命中", "没有命中", "未选择", "没有选择", "未发生", "没有发生", "重复调用", "被重复调用",
	"未设置", "没有设置", "少必填字段", "缺少必填字段", "无命中", "没有命中", "召回无关", "内容无关",
	"覆盖率低", "非json", "不是json",
	"不完全正确", "并不正确", "不正确", "不对", "不合法", "不充分", "不够", "不完整", "不正常",
	"不匹配", "不一致", "不符合", "不符", "没有成功", "未成功", "未调用", "没有调用", "未被调用",
	"存在问题", "有问题", "召回不足", "知识不足", "无法解析", "不能解析", "解析失败", "没有文档",
	"无文档", "未召回", "没有召回", "未检索", "没有检索", "空结果", "零篇", "0篇", "零个", "0个",
	"与预期不同", "不同", "有差异", "差异", "较差", "很差", "薄弱", "超时", "错误", "有误",
	"选错", "调用错", "参数错", "无效", "不足", "缺失", "缺少", "遗漏", "失败", "出错", "异常",
	"问题", "错",
}

func rubricPolarityCues(clause string) []rubricTextSpan {
	positive := findRubricCueSpans(clause, rubricPositiveCueTerms, rubricPolarityPositive)
	negative := findRubricCueSpans(clause, rubricNegativeCueTerms, rubricPolarityNegative)

	// A longer phrase carries the scoped meaning when opposite-polarity cues
	// overlap: "no error" is positive, while "not correct" is negative.
	positive = removeRubricCueOverlaps(positive, negative)
	negative = removeRubricCueOverlaps(negative, positive)
	cues := append(positive, negative...)
	for i := range cues {
		if rubricNegationCount(clause, cues[i].start)%2 != 0 {
			cues[i].polarity = -cues[i].polarity
		}
	}
	return cues
}

func findRubricCueSpans(text string, terms []string, polarity rubricPolarity) []rubricTextSpan {
	spans := findRubricTermSpans(text, terms, "")
	for i := range spans {
		spans[i].polarity = polarity
	}
	return spans
}

func removeRubricCueOverlaps(
	candidates []rubricTextSpan,
	opposite []rubricTextSpan,
) []rubricTextSpan {
	filtered := candidates[:0]
	for _, candidate := range candidates {
		keep := true
		for _, other := range opposite {
			if candidate.start >= other.end || other.start >= candidate.end {
				continue
			}
			candidateLength := candidate.end - candidate.start
			otherLength := other.end - other.start
			if otherLength > candidateLength || otherLength == candidateLength && other.start <= candidate.start {
				keep = false
				break
			}
		}
		if keep {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func rubricNegationCount(clause string, cueStart int) int {
	prefix := rubricNegationScopePrefix(clause, cueStart)
	return rubricEnglishNegationCount(prefix) + rubricChineseNegationCount(prefix)
}

func rubricNegationScopePrefix(clause string, cueStart int) string {
	prefix := clause[:cueStart]
	start := 0
	for _, separator := range []string{
		",", "，", ":", "：", " -- ", " — ",
		" and ", " or ", " but ", " however ", " whereas ", " while ", " yet ", " although ", " though ",
		"但是", "不过", "然而", "并且", "以及", "但", "而", "且", "和", "与",
	} {
		if index := strings.LastIndex(prefix, separator); index >= start {
			start = index + len(separator)
		}
	}
	return prefix[start:]
}

func rubricEnglishNegationCount(prefix string) int {
	tokens := rubricEnglishTokens(prefix)
	if len(tokens) > 7 {
		tokens = tokens[len(tokens)-7:]
	}
	count := 0
	for i, token := range tokens {
		switch {
		case token == "not" && i+1 < len(tokens) &&
			(tokens[i+1] == "only" || tokens[i+1] == "just" || tokens[i+1] == "merely"):
			continue
		case token == "no", token == "not", token == "never", token == "neither",
			token == "without", token == "hardly", token == "scarcely", token == "cannot",
			token == "nothing", token == "nowhere", strings.HasSuffix(token, "n't"):
			count++
		case token == "free" && i+1 < len(tokens) && tokens[i+1] == "of":
			count++
		}
	}
	return count
}

func rubricEnglishTokens(text string) []string {
	var tokens []string
	start := -1
	for i := 0; i <= len(text); i++ {
		wordByte := i < len(text) && (rubricASCIIWordByte(text[i]) || text[i] == '\'')
		if wordByte && start < 0 {
			start = i
		}
		if !wordByte && start >= 0 {
			tokens = append(tokens, text[start:i])
			start = -1
		}
	}
	return tokens
}

func rubricChineseNegationCount(prefix string) int {
	runes := []rune(prefix)
	if len(runes) > 18 {
		prefix = string(runes[len(runes)-18:])
	}
	markers := []string{
		"从来没有", "并不是", "并没有", "不存在", "未发现", "并未", "并非", "并不",
		"从未", "未曾", "没有", "不是", "不算", "无法", "不能", "没能", "不曾",
		"没", "未", "无", "不",
	}
	occupied := make([]bool, len(prefix))
	count := 0
	for _, marker := range markers {
		for offset := 0; offset < len(prefix); {
			index := strings.Index(prefix[offset:], marker)
			if index < 0 {
				break
			}
			start := offset + index
			end := start + len(marker)
			offset = end
			if rubricChineseNonNegatingPhrase(prefix[start:]) {
				continue
			}
			overlapped := false
			for i := start; i < end; i++ {
				if occupied[i] {
					overlapped = true
					break
				}
			}
			if overlapped {
				continue
			}
			for i := start; i < end; i++ {
				occupied[i] = true
			}
			count++
		}
	}
	return count
}

func rubricChineseNonNegatingPhrase(text string) bool {
	for _, prefix := range []string{"不但", "不仅", "不只", "不论", "不管", "无论"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func nearestRubricTopics(
	clause string,
	cue rubricTextSpan,
	topics []rubricTextSpan,
) []rubricTextSpan {
	if len(topics) == 0 {
		return nil
	}
	minimumDistance := rubricSpanDistance(cue, topics[0])
	for _, topic := range topics[1:] {
		if distance := rubricSpanDistance(cue, topic); distance < minimumDistance {
			minimumDistance = distance
		}
	}
	owners := make([]rubricTextSpan, 0, len(topics))
	owned := make([]bool, len(topics))
	for i, topic := range topics {
		if rubricSpanDistance(cue, topic) == minimumDistance {
			owners = append(owners, topic)
			owned[i] = true
		}
	}
	// A shared predicate applies to coordinated topics even when one is a few
	// bytes closer: "route and tool call failed" owns both topics. Expand to a
	// fixed point so lists of three or more coordinated topics also work.
	for changed := true; changed; {
		changed = false
		for i, topic := range topics {
			if owned[i] {
				continue
			}
			for _, owner := range owners {
				if rubricTopicsSharePredicate(clause, topic, owner) ||
					rubricTopicsFormPhrase(clause, topic, owner) {
					owners = append(owners, topic)
					owned[i] = true
					changed = true
					break
				}
			}
		}
	}
	return owners
}

func rubricTopicsFormPhrase(clause string, left, right rubricTextSpan) bool {
	if left.category != right.category {
		return false
	}
	if left.start > right.start {
		left, right = right, left
	}
	between := strings.TrimSpace(clause[left.end:right.start])
	return between == "" || between == "-" || between == "的"
}

func rubricTopicsSharePredicate(clause string, left, right rubricTextSpan) bool {
	if left.start > right.start {
		left, right = right, left
	}
	between := strings.TrimSpace(clause[left.end:right.start])
	matchedConnector := false
	for _, connector := range []string{
		"as well as", "along with", "together with", "and", "or", "&", "/", ",",
		"以及", "还有", "和", "与", "及", "、", "，",
	} {
		if strings.Contains(between, connector) {
			matchedConnector = true
			between = strings.ReplaceAll(between, connector, " ")
		}
	}
	for _, article := range []string{"the", "a", "an", "both", "该", "此", "这个", "这些", "两者", "均", "都"} {
		between = strings.ReplaceAll(" "+strings.TrimSpace(between)+" ", " "+article+" ", " ")
	}
	return matchedConnector && strings.TrimSpace(between) == ""
}

func rubricSpanDistance(left, right rubricTextSpan) int {
	if left.end <= right.start {
		return right.start - left.end
	}
	if right.end <= left.start {
		return left.start - right.end
	}
	return 0
}
