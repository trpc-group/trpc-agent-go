//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"reflect"
	"sort"
	"testing"
)

func TestRubricAttributionScopesCorrectSignalsAwayFromFinalFailure(t *testing.T) {
	tests := []struct {
		name   string
		reason string
	}{
		{
			name:   "english route no problem",
			reason: "The routing had no problems and the final answer was wrong.",
		},
		{
			name:   "chinese route no problem",
			reason: "路由没有任何问题，但最终答案错误。",
		},
		{
			name:   "english tool correct",
			reason: "Tool execution was correct; only the final answer was incorrect.",
		},
		{
			name:   "chinese tool correct",
			reason: "工具调用正确，最终答案错误。",
		},
		{
			name:   "english parameter correct",
			reason: "The tool parameters were valid, while the final answer was wrong.",
		},
		{
			name:   "chinese parameter correct",
			reason: "工具参数没有问题，只是最终答案错误。",
		},
		{
			name:   "english format valid",
			reason: "The JSON format was valid but the final answer was incorrect.",
		},
		{
			name:   "chinese format valid",
			reason: "结构化输出格式合法，最终答案错误。",
		},
		{
			name:   "english retrieval sufficient",
			reason: "Knowledge retrieval was sufficient; the final answer was wrong.",
		},
		{
			name:   "chinese retrieval sufficient",
			reason: "知识检索和召回充分，但最终答案错误。",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRubricCategories(t, test.reason, FailureFinalResponseMismatch)
		})
	}
}

func TestRubricAttributionStillClassifiesActualFailures(t *testing.T) {
	tests := []struct {
		name         string
		reason       string
		wantCategory FailureCategory
	}{
		{name: "english route", reason: "The route selected the wrong agent.", wantCategory: FailureRouteError},
		{name: "chinese route", reason: "路由选择错误。", wantCategory: FailureRouteError},
		{name: "english tool", reason: "The tool call failed.", wantCategory: FailureToolCallError},
		{name: "chinese tool", reason: "工具调用失败。", wantCategory: FailureToolCallError},
		{name: "english parameter", reason: "The tool parameters were incorrect.", wantCategory: FailureToolParameterError},
		{name: "chinese parameter", reason: "工具参数错误。", wantCategory: FailureToolParameterError},
		{name: "english format", reason: "The JSON format was invalid.", wantCategory: FailureFormatError},
		{name: "chinese format", reason: "结构化输出格式不合法。", wantCategory: FailureFormatError},
		{name: "english retrieval", reason: "Knowledge retrieval was insufficient.", wantCategory: FailureKnowledgeRetrievalInsufficient},
		{name: "chinese retrieval", reason: "知识检索召回不足。", wantCategory: FailureKnowledgeRetrievalInsufficient},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attributions := assertRubricCategories(
				t,
				test.reason,
				test.wantCategory,
				FailureFinalResponseMismatch,
			)
			if len(attributions) == 0 || attributions[0].Category != test.wantCategory {
				t.Fatalf("primary attribution = %+v, want %q", attributions, test.wantCategory)
			}
		})
	}
}

func TestRubricAttributionHandlesNegatedPositivePredicates(t *testing.T) {
	tests := []struct {
		name         string
		reason       string
		wantCategory FailureCategory
	}{
		{name: "english route not correct", reason: "The route was not correct.", wantCategory: FailureRouteError},
		{name: "chinese parameter not correct", reason: "工具参数不正确。", wantCategory: FailureToolParameterError},
		{name: "english format not valid", reason: "The JSON schema was not valid.", wantCategory: FailureFormatError},
		{name: "chinese retrieval not sufficient", reason: "知识召回不充分。", wantCategory: FailureKnowledgeRetrievalInsufficient},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			attributions := assertRubricCategories(
				t,
				test.reason,
				test.wantCategory,
				FailureFinalResponseMismatch,
			)
			if len(attributions) == 0 || attributions[0].Category != test.wantCategory {
				t.Fatalf("primary attribution = %+v, want %q", attributions, test.wantCategory)
			}
		})
	}
}

func TestRubricAttributionAssociatesPredicatesWithTheirNearestTopic(t *testing.T) {
	t.Run("answer predicate does not leak to neutral route mention", func(t *testing.T) {
		assertRubricCategories(t,
			"The route was selected and the final answer was wrong.",
			FailureFinalResponseMismatch,
		)
	})

	t.Run("shared predicate applies to both coordinated topics", func(t *testing.T) {
		attributions := assertRubricCategories(t,
			"The route and final answer were wrong.",
			FailureRouteError,
			FailureFinalResponseMismatch,
		)
		if len(attributions) == 0 || attributions[0].Category != FailureRouteError {
			t.Fatalf("shared route failure was not classified: %+v", attributions)
		}
	})

	t.Run("opposite predicates stay with their own topics", func(t *testing.T) {
		attributions := assertRubricCategories(t,
			"The route was correct and the tool call was wrong.",
			FailureToolCallError,
			FailureFinalResponseMismatch,
		)
		if len(attributions) == 0 || attributions[0].Category != FailureToolCallError {
			t.Fatalf("tool failure primary attribution = %+v", attributions)
		}
	})
}

func TestRubricAttributionHandlesEnglishNegationScope(t *testing.T) {
	positive := []string{
		"The route wasn't wrong; the final answer was wrong.",
		"The route wasn’t wrong; the final answer was wrong.",
		"The route was not an issue; the final answer was wrong.",
		"No route error was found; the final answer was wrong.",
		"The tool call never failed; the final answer was wrong.",
		"Retrieval was not insufficient; the final answer was wrong.",
	}
	for _, reason := range positive {
		t.Run(reason, func(t *testing.T) {
			assertRubricCategories(t, reason, FailureFinalResponseMismatch)
		})
	}

	negative := []struct {
		reason string
		want   FailureCategory
	}{
		{reason: "The route wasn't correct.", want: FailureRouteError},
		{reason: "The tool wasn't called.", want: FailureToolCallError},
		{reason: "The tool was never called.", want: FailureToolCallError},
		{reason: "The JSON schema wasn't valid.", want: FailureFormatError},
		{reason: "The structured output was not well-formed.", want: FailureFormatError},
		{reason: "Knowledge retrieval wasn't sufficient.", want: FailureKnowledgeRetrievalInsufficient},
	}
	for _, test := range negative {
		t.Run(test.reason, func(t *testing.T) {
			assertRubricCategories(t, test.reason, test.want, FailureFinalResponseMismatch)
		})
	}
}

func TestRubricAttributionHandlesChineseNegationScope(t *testing.T) {
	positive := []string{
		"路由没有任何错误，但最终答案错误。",
		"路由无明显错误，但最终答案错误。",
		"路由不是错误的，但最终答案错误。",
		"工具调用未出错，但最终答案错误。",
		"工具调用没有出错，但最终答案错误。",
		"工具参数没有缺失，但最终答案错误。",
		"格式并非无效，但最终答案错误。",
		"知识检索并非不足，但最终答案错误。",
	}
	for _, reason := range positive {
		t.Run(reason, func(t *testing.T) {
			assertRubricCategories(t, reason, FailureFinalResponseMismatch)
		})
	}

	negative := []struct {
		reason string
		want   FailureCategory
	}{
		{reason: "没有正确路由。", want: FailureRouteError},
		{reason: "未正确调用工具。", want: FailureToolCallError},
		{reason: "工具没有被调用。", want: FailureToolCallError},
		{reason: "工具参数未正确匹配。", want: FailureToolParameterError},
		{reason: "格式不正常。", want: FailureFormatError},
		{reason: "知识召回未充分。", want: FailureKnowledgeRetrievalInsufficient},
		{reason: "无法有效检索知识。", want: FailureKnowledgeRetrievalInsufficient},
	}
	for _, test := range negative {
		t.Run(test.reason, func(t *testing.T) {
			assertRubricCategories(t, test.reason, test.want, FailureFinalResponseMismatch)
		})
	}
}

func TestRubricAttributionAssignsCuesAcrossAllTopics(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		want   []FailureCategory
	}{
		{
			name:   "neutral route before format failure",
			reason: "The route was selected and the JSON format was invalid.",
			want:   []FailureCategory{FailureFormatError, FailureFinalResponseMismatch},
		},
		{
			name:   "tool modifier does not duplicate parameter failure",
			reason: "The tool parameters were incorrect.",
			want:   []FailureCategory{FailureToolParameterError, FailureFinalResponseMismatch},
		},
		{
			name:   "neutral tool before parameter failure",
			reason: "The tool call completed and the parameters were incorrect.",
			want:   []FailureCategory{FailureToolParameterError, FailureFinalResponseMismatch},
		},
		{
			name:   "neutral route before tool failure",
			reason: "The route was selected and the tool call failed.",
			want:   []FailureCategory{FailureToolCallError, FailureFinalResponseMismatch},
		},
		{
			name:   "shared failure predicate",
			reason: "The route and tool call failed.",
			want: []FailureCategory{
				FailureRouteError,
				FailureToolCallError,
				FailureFinalResponseMismatch,
			},
		},
		{
			name:   "three coordinated topics",
			reason: "The route, tool call, and JSON format were invalid.",
			want: []FailureCategory{
				FailureRouteError,
				FailureToolCallError,
				FailureFormatError,
				FailureFinalResponseMismatch,
			},
		},
		{
			name:   "earlier route predicate does not leak to tool",
			reason: "The route was wrong and the tool call completed.",
			want:   []FailureCategory{FailureRouteError, FailureFinalResponseMismatch},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRubricCategories(t, test.reason, test.want...)
		})
	}
}

func TestRubricAttributionPreservesLabelsContextAndFailureVocabulary(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		want   FailureCategory
	}{
		{name: "english colon label", reason: "Route: wrong agent.", want: FailureRouteError},
		{name: "chinese colon label", reason: "路由：错误。", want: FailureRouteError},
		{name: "while context", reason: "Failed while invoking the tool.", want: FailureToolCallError},
		{name: "while format context", reason: "Invalid while parsing JSON.", want: FailureFormatError},
		{name: "english timeout", reason: "The tool invocation timed out.", want: FailureToolCallError},
		{name: "chinese timeout", reason: "工具调用超时。", want: FailureToolCallError},
		{name: "english parameter differs", reason: "The parameters differed from expected values.", want: FailureToolParameterError},
		{name: "chinese parameter differs", reason: "参数与预期不同。", want: FailureToolParameterError},
		{name: "english zero retrieval", reason: "Knowledge retrieval returned zero documents.", want: FailureKnowledgeRetrievalInsufficient},
		{name: "chinese zero retrieval", reason: "知识检索返回零篇文档。", want: FailureKnowledgeRetrievalInsufficient},
		{name: "english unparseable format", reason: "The JSON format could not be parsed.", want: FailureFormatError},
		{name: "chinese unparseable format", reason: "结构化输出格式无法解析。", want: FailureFormatError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRubricCategories(t, test.reason, test.want, FailureFinalResponseMismatch)
		})
	}
}

func TestRubricAttributionHandlesContrastiveTopicNegation(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		want   FailureCategory
	}{
		{
			name:   "english excludes route",
			reason: "The tool call, not the route, failed.",
			want:   FailureToolCallError,
		},
		{
			name:   "chinese excludes tool",
			reason: "错误的不是工具调用，而是路由。",
			want:   FailureRouteError,
		},
		{
			name:   "chinese problem is tool",
			reason: "问题不在路由，而在工具调用。",
			want:   FailureToolCallError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRubricCategories(t, test.reason, test.want, FailureFinalResponseMismatch)
		})
	}
}

func TestRubricAttributionIgnoresResolvedHistoricalFailures(t *testing.T) {
	for _, reason := range []string{
		"The route error was fixed.",
		"The route error was resolved, but the final answer was wrong.",
		"路由错误已经修复。",
		"路由错误已解决，但最终答案错误。",
	} {
		t.Run(reason, func(t *testing.T) {
			assertRubricCategories(t, reason, FailureFinalResponseMismatch)
		})
	}
}

func TestRubricAttributionKeepsUnresolvedOrRecurringFailures(t *testing.T) {
	tests := []struct {
		reason string
		want   FailureCategory
	}{
		{reason: "The route error was not fixed.", want: FailureRouteError},
		{reason: "The route error was fixed, then failed again.", want: FailureRouteError},
		{reason: "路由错误未修复。", want: FailureRouteError},
		{reason: "路由错误已经修复，但随后再次失败。", want: FailureRouteError},
	}
	for _, test := range tests {
		t.Run(test.reason, func(t *testing.T) {
			assertRubricCategories(t, test.reason, test.want, FailureFinalResponseMismatch)
		})
	}
}

func TestRubricAttributionContrastAndVocabularyGuards(t *testing.T) {
	tests := []struct {
		reason string
		want   []FailureCategory
	}{
		{
			reason: "The problem was not the route but the tool call.",
			want:   []FailureCategory{FailureToolCallError, FailureFinalResponseMismatch},
		},
		{
			reason: "The route, rather than the tool call, failed.",
			want:   []FailureCategory{FailureRouteError, FailureFinalResponseMismatch},
		},
		{
			reason: "No tool call was required; the final answer was wrong.",
			want:   []FailureCategory{FailureFinalResponseMismatch},
		},
		{
			reason: "The JSON format was valid; the final answer was wrong.",
			want:   []FailureCategory{FailureFinalResponseMismatch},
		},
		{
			reason: "The search query parameter was wrong.",
			want:   []FailureCategory{FailureToolParameterError, FailureFinalResponseMismatch},
		},
	}
	for _, test := range tests {
		t.Run(test.reason, func(t *testing.T) {
			assertRubricCategories(t, test.reason, test.want...)
		})
	}
}

func TestRubricAttributionClassifiesConcreteFailureCues(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		want   FailureCategory
	}{
		{name: "routing did not select", reason: "Routing did not select the specialist agent.", want: FailureRouteError},
		{name: "routing did not hit", reason: "Routing did not hit the billing agent.", want: FailureRouteError},
		{name: "route missed agent", reason: "路由未命中代理。", want: FailureRouteError},
		{name: "tool never happened", reason: "The tool call never happened.", want: FailureToolCallError},
		{name: "tool did not happen", reason: "工具调用未发生。", want: FailureToolCallError},
		{name: "tool repeated", reason: "The tool call was repeated.", want: FailureToolCallError},
		{name: "tool repeated chinese", reason: "工具被重复调用。", want: FailureToolCallError},
		{name: "parameter never set", reason: "The parameter was never set.", want: FailureToolParameterError},
		{name: "parameter unset chinese", reason: "参数未设置。", want: FailureToolParameterError},
		{name: "tool input incorrect", reason: "The tool input was incorrect.", want: FailureToolParameterError},
		{name: "tool input incorrect chinese", reason: "工具输入不正确。", want: FailureToolParameterError},
		{name: "output unparseable", reason: "The output could not be parsed.", want: FailureFormatError},
		{name: "required field missing", reason: "结构化输出少必填字段。", want: FailureFormatError},
		{name: "plain text not json", reason: "输出是普通文本非JSON。", want: FailureFormatError},
		{name: "no relevant docs", reason: "Retrieval returned no relevant docs.", want: FailureKnowledgeRetrievalInsufficient},
		{name: "no hit", reason: "知识检索无命中。", want: FailureKnowledgeRetrievalInsufficient},
		{name: "irrelevant recall", reason: "召回内容无关。", want: FailureKnowledgeRetrievalInsufficient},
		{name: "low coverage", reason: "Retrieval coverage was low.", want: FailureKnowledgeRetrievalInsufficient},
		{name: "low coverage chinese", reason: "知识召回覆盖率低。", want: FailureKnowledgeRetrievalInsufficient},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRubricCategories(t, test.reason, test.want, FailureFinalResponseMismatch)
		})
	}
}

func TestTraceAttributionUsesLastSuccessfulRoute(t *testing.T) {
	tests := []struct {
		name  string
		trace []TraceStep
		want  []FailureCategory
	}{
		{
			name: "intermediate route recovered",
			trace: []TraceStep{
				{StepID: "route-general", Kind: "route", Name: "general", Status: "completed"},
				{StepID: "route-specialist", Kind: "handoff", Name: "specialist", Status: "completed"},
			},
			want: []FailureCategory{FailureFinalResponseMismatch},
		},
		{
			name: "last successful route is wrong",
			trace: []TraceStep{
				{StepID: "route-specialist", Kind: "route", Name: "specialist", Status: "completed"},
				{StepID: "route-general", Kind: "handoff", Name: "general", Status: "completed"},
			},
			want: []FailureCategory{FailureRouteError, FailureFinalResponseMismatch},
		},
		{
			name: "failed route remains evidence",
			trace: []TraceStep{
				{StepID: "route-general", Kind: "route", Name: "general", Status: "failed"},
				{StepID: "route-specialist", Kind: "handoff", Name: "specialist", Status: "completed"},
			},
			want: []FailureCategory{FailureRouteError, FailureFinalResponseMismatch},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := rubricOnlyFailure("The final answer was wrong.")
			result.ExpectedRoute = "specialist"
			result.Route = "specialist"
			result.Trace = test.trace
			attributions := AttributeFailure(result)
			got := make([]string, 0, len(attributions))
			for _, attribution := range attributions {
				got = append(got, string(attribution.Category))
			}
			want := make([]string, 0, len(test.want))
			for _, category := range test.want {
				want = append(want, string(category))
			}
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("attribution categories = %v, want %v; full = %+v", got, want, attributions)
			}
		})
	}
}

func assertRubricCategories(
	t *testing.T,
	reason string,
	want ...FailureCategory,
) []Attribution {
	t.Helper()
	attributions := AttributeFailure(rubricOnlyFailure(reason))
	gotCategories := make([]string, 0, len(attributions))
	for _, attribution := range attributions {
		gotCategories = append(gotCategories, string(attribution.Category))
	}
	wantCategories := make([]string, 0, len(want))
	for _, category := range want {
		wantCategories = append(wantCategories, string(category))
	}
	sort.Strings(gotCategories)
	sort.Strings(wantCategories)
	if !reflect.DeepEqual(gotCategories, wantCategories) {
		t.Fatalf("attribution categories for %q = %v, want %v; full = %+v", reason, gotCategories, wantCategories, attributions)
	}
	return attributions
}

func rubricOnlyFailure(reason string) CaseResult {
	return CaseResult{
		Passed:       false,
		RubricReason: reason,
		MetricResults: []MetricResult{
			{
				MetricName: metricFinalResponse,
				Score:      0,
				Threshold:  1,
				Weight:     1,
				Passed:     false,
			},
			{
				MetricName: metricLLMRubric,
				Score:      0,
				Threshold:  1,
				Weight:     1,
				Passed:     false,
			},
		},
	}
}
