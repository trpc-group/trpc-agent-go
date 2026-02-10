//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package finalresponse

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	criterionrouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TestFinalResponseCriterion_JSONRoundTrip verifies JSON marshal and unmarshal behavior for the criterion.
func TestFinalResponseCriterion_JSONRoundTrip(t *testing.T) {
	c := &FinalResponseCriterion{
		Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyContains},
		Rouge: &criterionrouge.RougeCriterion{
			RougeType:  "rouge1",
			Measure:    criterionrouge.RougeMeasureF1,
			UseStemmer: true,
		},
	}
	data, err := json.Marshal(c)
	assert.NoError(t, err)

	var decoded FinalResponseCriterion
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.NotNil(t, decoded.Text)
	assert.Equal(t, text.TextMatchStrategyContains, decoded.Text.MatchStrategy)
	assert.NotNil(t, decoded.Rouge)
	assert.Equal(t, "rouge1", decoded.Rouge.RougeType)
	assert.Equal(t, criterionrouge.RougeMeasureF1, decoded.Rouge.Measure)
	assert.True(t, decoded.Rouge.UseStemmer)
}

// TestFinalResponseCriterion_EmptyCriteriaError verifies that missing sub-criteria returns an error.
func TestFinalResponseCriterion_EmptyCriteriaError(t *testing.T) {
	criterion := &FinalResponseCriterion{}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: "ok"}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: "ok"}}
	ok, err := criterion.Match(context.Background(), actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must configure text, json, or rouge")
}

// TestFinalResponseCriterion_TextMismatch verifies mismatch reporting for text criteria.
func TestFinalResponseCriterion_TextMismatch(t *testing.T) {
	criterion := &FinalResponseCriterion{
		Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: "a"}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: "b"}}
	ok, err := criterion.Match(context.Background(), actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

// TestFinalResponseCriterion_JSONMatch verifies successful JSON matching when values are equivalent.
func TestFinalResponseCriterion_JSONMatch(t *testing.T) {
	criterion := &FinalResponseCriterion{
		JSON: criterionjson.New(),
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":1,"b":[2,3]}`}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"b":[2,3],"a":1}`}}
	ok, err := criterion.Match(context.Background(), actual, expected)
	assert.NoError(t, err)
	assert.True(t, ok)
}

// TestFinalResponseCriterion_JSONParseError verifies JSON parsing failures are returned as errors.
func TestFinalResponseCriterion_JSONParseError(t *testing.T) {
	criterion := &FinalResponseCriterion{
		JSON: criterionjson.New(),
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: `not json`}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: `{}`}}
	ok, err := criterion.Match(context.Background(), actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

// TestFinalResponseCriterion_TextAndJSONCriteria_BothPass verifies that both text and JSON criteria can pass together.
func TestFinalResponseCriterion_TextAndJSONCriteria_BothPass(t *testing.T) {
	criterion := &FinalResponseCriterion{
		Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
		JSON: criterionjson.New(),
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":1}`}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":1}`}}
	ok, err := criterion.Match(context.Background(), actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

// TestFinalResponseCriterion_TextAndJSONCriteria_TextFails verifies that text mismatches are reported when text fails.
func TestFinalResponseCriterion_TextAndJSONCriteria_TextFails(t *testing.T) {
	criterion := &FinalResponseCriterion{
		Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
		JSON: criterionjson.New(),
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":1}`}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: "{\n  \"a\": 1\n}"}}
	ok, err := criterion.Match(context.Background(), actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "text mismatch")
}

// TestFinalResponseCriterion_TextAndJSONCriteria_JSONFails verifies that JSON mismatches are reported when JSON fails.
func TestFinalResponseCriterion_TextAndJSONCriteria_JSONFails(t *testing.T) {
	criterion := &FinalResponseCriterion{
		Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyContains},
		JSON: criterionjson.New(),
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":1}`}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: `"a"`}}
	ok, err := criterion.Match(context.Background(), actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "json mismatch")
}

// TestFinalResponseCriterion_CustomCompare verifies the custom compare override is executed.
func TestFinalResponseCriterion_CustomCompare(t *testing.T) {
	called := false
	criterion := &FinalResponseCriterion{
		Compare: func(actual, expected *evalset.Invocation) (bool, error) {
			called = true
			return actual.InvocationID == expected.InvocationID, nil
		},
	}
	actual := &evalset.Invocation{InvocationID: "same"}
	expected := &evalset.Invocation{InvocationID: "same"}
	ok, err := criterion.Match(context.Background(), actual, expected)
	assert.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, called)
}

// TestFinalResponseCriterion_NilReceiver verifies nil receiver handling.
func TestFinalResponseCriterion_NilReceiver(t *testing.T) {
	var criterion *FinalResponseCriterion
	ok, err := criterion.Match(context.Background(), nil, nil)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "final response criterion is nil")
}

// TestFinalResponseCriterion_NewAppliesOptions verifies that constructor options are applied as expected.
func TestFinalResponseCriterion_NewAppliesOptions(t *testing.T) {
	called := false
	compare := func(actual, expected *evalset.Invocation) (bool, error) {
		called = true
		return actual == nil && expected == nil, nil
	}
	textCriterion := &text.TextCriterion{MatchStrategy: text.TextMatchStrategyContains}
	jsonCriterion := criterionjson.New()
	rougeCriterion := &criterionrouge.RougeCriterion{RougeType: "rouge1", Measure: criterionrouge.RougeMeasureF1}

	criterion := New(
		WithTextCriterion(textCriterion),
		WithJSONCriterion(jsonCriterion),
		WithRougeCriterion(rougeCriterion),
		WithCompare(compare),
	)

	assert.Same(t, textCriterion, criterion.Text)
	assert.Same(t, jsonCriterion, criterion.JSON)
	assert.Same(t, rougeCriterion, criterion.Rouge)

	ok, err := criterion.Match(context.Background(), nil, nil)
	assert.True(t, ok)
	assert.NoError(t, err)
	assert.True(t, called)
}

// TestFinalResponseCriterion_NilInvocations verifies error handling for nil invocations.
func TestFinalResponseCriterion_NilInvocations(t *testing.T) {
	criterion := &FinalResponseCriterion{Text: &text.TextCriterion{Ignore: true}}
	ok, err := criterion.Match(context.Background(), nil, &evalset.Invocation{})
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "actual or expected invocation is nil")

	ok, err = criterion.Match(context.Background(), &evalset.Invocation{}, nil)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "actual or expected invocation is nil")
}

// TestFinalResponseCriterion_BothFinalResponseNil verifies matching when both final responses are nil.
func TestFinalResponseCriterion_BothFinalResponseNil(t *testing.T) {
	criterion := &FinalResponseCriterion{Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact}}
	actual := &evalset.Invocation{}
	expected := &evalset.Invocation{}
	ok, err := criterion.Match(context.Background(), actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

// TestFinalResponseCriterion_OneFinalResponseNil verifies mismatch behavior when one final response is nil.
func TestFinalResponseCriterion_OneFinalResponseNil(t *testing.T) {
	criterion := &FinalResponseCriterion{Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact}}
	actual := &evalset.Invocation{}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: "ok"}}
	ok, err := criterion.Match(context.Background(), actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "actual or expected final response is nil")
}

// TestFinalResponseCriterion_TextAndJSONCriteria_BothFail verifies aggregation of mismatches across criteria.
func TestFinalResponseCriterion_TextAndJSONCriteria_BothFail(t *testing.T) {
	criterion := &FinalResponseCriterion{
		Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
		JSON: criterionjson.New(),
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":1}`}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":2}`}}
	ok, err := criterion.Match(context.Background(), actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "json mismatch")
	assert.Contains(t, err.Error(), "text mismatch")
	assert.Contains(t, err.Error(), "; ")
}

// TestMatchContentAsText_Ignored verifies that nil or ignored criteria do not error.
func TestMatchContentAsText_Ignored(t *testing.T) {
	err := matchContentAsText("a", "b", nil)
	assert.NoError(t, err)

	err = matchContentAsText("a", "b", &text.TextCriterion{Ignore: true})
	assert.NoError(t, err)
}

// TestMatchContentAsText_CustomCompareFalseNoError verifies handling when a custom compare returns false without error.
func TestMatchContentAsText_CustomCompareFalseNoError(t *testing.T) {
	criterion := &text.TextCriterion{
		Compare: func(actual, expected string) (bool, error) {
			return false, nil
		},
	}
	err := matchContentAsText("a", "b", criterion)
	assert.EqualError(t, err, "text mismatch")
}

// TestMatchContentAsJSON_Ignored verifies that nil or ignored criteria do not error.
func TestMatchContentAsJSON_Ignored(t *testing.T) {
	err := matchContentAsJSON("not json", "still not json", nil)
	assert.NoError(t, err)

	err = matchContentAsJSON("not json", "still not json", &criterionjson.JSONCriterion{Ignore: true})
	assert.NoError(t, err)
}

// TestMatchContentAsJSON_CustomCompareFalseNoError verifies handling when a custom compare returns false without error.
func TestMatchContentAsJSON_CustomCompareFalseNoError(t *testing.T) {
	criterion := &criterionjson.JSONCriterion{
		Compare: func(actual, expected any) (bool, error) {
			return false, nil
		},
	}
	err := matchContentAsJSON(`{"a":1}`, `{"a":1}`, criterion)
	assert.EqualError(t, err, "json mismatch")
}

// TestMatchContentAsJSON_ExpectedParseError verifies error reporting when expected JSON is invalid.
func TestMatchContentAsJSON_ExpectedParseError(t *testing.T) {
	criterion := criterionjson.New()
	err := matchContentAsJSON(`{"a":1}`, `not json`, criterion)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse expected final response as json")
}

// TestMatchContentAsRouge_Ignored verifies that nil or ignored criteria do not error.
func TestMatchContentAsRouge_Ignored(t *testing.T) {
	err := matchContentAsRouge(context.Background(), "a", "b", nil)
	assert.NoError(t, err)

	err = matchContentAsRouge(context.Background(), "a", "b", &criterionrouge.RougeCriterion{Ignore: true})
	assert.NoError(t, err)
}

// TestMatchContentAsRouge_Passed verifies that content comparisons pass when thresholds are satisfied.
func TestMatchContentAsRouge_Passed(t *testing.T) {
	criterion := &criterionrouge.RougeCriterion{
		RougeType: "rouge1",
		Measure:   criterionrouge.RougeMeasureF1,
		Threshold: criterionrouge.Score{F1: 0.5},
	}
	err := matchContentAsRouge(context.Background(), "testing", "testing one two", criterion)
	assert.NoError(t, err)
}

// TestMatchContentAsRouge_Failed verifies mismatch reporting when content does not satisfy thresholds.
func TestMatchContentAsRouge_Failed(t *testing.T) {
	criterion := &criterionrouge.RougeCriterion{
		RougeType: "rouge1",
		Measure:   criterionrouge.RougeMeasureF1,
		Threshold: criterionrouge.Score{F1: 0.6},
	}
	err := matchContentAsRouge(context.Background(), "testing", "testing one two", criterion)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rouge mismatch")
	assert.Contains(t, err.Error(), "rouge1")
}
