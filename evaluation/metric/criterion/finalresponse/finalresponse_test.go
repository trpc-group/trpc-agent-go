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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestFinalResponseCriterion_JSONRoundTrip(t *testing.T) {
	c := &FinalResponseCriterion{
		Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyContains},
	}
	data, err := json.Marshal(c)
	assert.NoError(t, err)

	var decoded FinalResponseCriterion
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.NotNil(t, decoded.Text)
	assert.Equal(t, text.TextMatchStrategyContains, decoded.Text.MatchStrategy)
}

func TestFinalResponseCriterion_EmptyCriteriaError(t *testing.T) {
	criterion := &FinalResponseCriterion{}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: "ok"}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: "ok"}}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must configure text and/or json")
}

func TestFinalResponseCriterion_TextMismatch(t *testing.T) {
	criterion := &FinalResponseCriterion{
		Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: "a"}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: "b"}}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestFinalResponseCriterion_JSONMatch(t *testing.T) {
	criterion := &FinalResponseCriterion{
		JSON: criterionjson.New(),
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":1,"b":[2,3]}`}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"b":[2,3],"a":1}`}}
	ok, err := criterion.Match(actual, expected)
	assert.NoError(t, err)
	assert.True(t, ok)
}

func TestFinalResponseCriterion_JSONParseError(t *testing.T) {
	criterion := &FinalResponseCriterion{
		JSON: criterionjson.New(),
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: `not json`}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: `{}`}}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestFinalResponseCriterion_TextAndJSONCriteria_BothPass(t *testing.T) {
	criterion := &FinalResponseCriterion{
		Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
		JSON: criterionjson.New(),
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":1}`}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":1}`}}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestFinalResponseCriterion_TextAndJSONCriteria_TextFails(t *testing.T) {
	criterion := &FinalResponseCriterion{
		Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
		JSON: criterionjson.New(),
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":1}`}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: "{\n  \"a\": 1\n}"}}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "text mismatch")
}

func TestFinalResponseCriterion_TextAndJSONCriteria_JSONFails(t *testing.T) {
	criterion := &FinalResponseCriterion{
		Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyContains},
		JSON: criterionjson.New(),
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":1}`}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: `"a"`}}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "json mismatch")
}

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
	ok, err := criterion.Match(actual, expected)
	assert.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, called)
}

func TestFinalResponseCriterion_NilReceiver(t *testing.T) {
	var criterion *FinalResponseCriterion
	ok, err := criterion.Match(nil, nil)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "final response criterion is nil")
}

func TestFinalResponseCriterion_NewAppliesOptions(t *testing.T) {
	called := false
	compare := func(actual, expected *evalset.Invocation) (bool, error) {
		called = true
		return actual == nil && expected == nil, nil
	}
	textCriterion := &text.TextCriterion{MatchStrategy: text.TextMatchStrategyContains}
	jsonCriterion := criterionjson.New()

	criterion := New(
		WithTextCriterion(textCriterion),
		WithJSONCriterion(jsonCriterion),
		WithCompare(compare),
	)

	assert.Same(t, textCriterion, criterion.Text)
	assert.Same(t, jsonCriterion, criterion.JSON)

	ok, err := criterion.Match(nil, nil)
	assert.True(t, ok)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestFinalResponseCriterion_NilInvocations(t *testing.T) {
	criterion := &FinalResponseCriterion{Text: &text.TextCriterion{Ignore: true}}
	ok, err := criterion.Match(nil, &evalset.Invocation{})
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "actual or expected invocation is nil")

	ok, err = criterion.Match(&evalset.Invocation{}, nil)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "actual or expected invocation is nil")
}

func TestFinalResponseCriterion_BothFinalResponseNil(t *testing.T) {
	criterion := &FinalResponseCriterion{Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact}}
	actual := &evalset.Invocation{}
	expected := &evalset.Invocation{}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestFinalResponseCriterion_OneFinalResponseNil(t *testing.T) {
	criterion := &FinalResponseCriterion{Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact}}
	actual := &evalset.Invocation{}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: "ok"}}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "actual or expected final response is nil")
}

func TestFinalResponseCriterion_TextAndJSONCriteria_BothFail(t *testing.T) {
	criterion := &FinalResponseCriterion{
		Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
		JSON: criterionjson.New(),
	}
	actual := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":1}`}}
	expected := &evalset.Invocation{FinalResponse: &model.Message{Content: `{"a":2}`}}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "json mismatch")
	assert.Contains(t, err.Error(), "text mismatch")
	assert.Contains(t, err.Error(), "; ")
}

func TestMatchContentAsText_Ignored(t *testing.T) {
	err := matchContentAsText("a", "b", nil)
	assert.NoError(t, err)

	err = matchContentAsText("a", "b", &text.TextCriterion{Ignore: true})
	assert.NoError(t, err)
}

func TestMatchContentAsText_CustomCompareFalseNoError(t *testing.T) {
	criterion := &text.TextCriterion{
		Compare: func(actual, expected string) (bool, error) {
			return false, nil
		},
	}
	err := matchContentAsText("a", "b", criterion)
	assert.EqualError(t, err, "text mismatch")
}

func TestMatchContentAsJSON_Ignored(t *testing.T) {
	err := matchContentAsJSON("not json", "still not json", nil)
	assert.NoError(t, err)

	err = matchContentAsJSON("not json", "still not json", &criterionjson.JSONCriterion{Ignore: true})
	assert.NoError(t, err)
}

func TestMatchContentAsJSON_CustomCompareFalseNoError(t *testing.T) {
	criterion := &criterionjson.JSONCriterion{
		Compare: func(actual, expected any) (bool, error) {
			return false, nil
		},
	}
	err := matchContentAsJSON(`{"a":1}`, `{"a":1}`, criterion)
	assert.EqualError(t, err, "json mismatch")
}

func TestMatchContentAsJSON_ExpectedParseError(t *testing.T) {
	criterion := criterionjson.New()
	err := matchContentAsJSON(`{"a":1}`, `not json`, criterion)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse expected final response as json")
}
