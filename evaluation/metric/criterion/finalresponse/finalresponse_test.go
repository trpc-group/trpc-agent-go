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
