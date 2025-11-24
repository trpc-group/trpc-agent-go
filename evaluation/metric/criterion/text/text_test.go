//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package text

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTextCriterionJSONRoundTrip(t *testing.T) {
	criterion := &TextCriterion{
		Ignore:          true,
		CaseInsensitive: true,
		MatchStrategy:   TextMatchStrategyRegex,
	}
	data, err := json.Marshal(criterion)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"ignore":true,"caseInsensitive":true,"matchStrategy":"regex"}`, string(data))

	var decoded TextCriterion
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, criterion.Ignore, decoded.Ignore)
	assert.Equal(t, criterion.CaseInsensitive, decoded.CaseInsensitive)
	assert.Equal(t, criterion.MatchStrategy, decoded.MatchStrategy)
}

func TestTextCriterionMatchStrategies(t *testing.T) {
	criterion := &TextCriterion{
		CaseInsensitive: true,
		MatchStrategy:   TextMatchStrategyContains,
	}
	ok, err := criterion.Match("Hello World", "hello")
	assert.NoError(t, err)
	assert.True(t, ok)
}

func TestTextCriterionIgnore(t *testing.T) {
	criterion := &TextCriterion{
		Ignore: true,
	}
	ok, err := criterion.Match("anything", "value")
	assert.NoError(t, err)
	assert.True(t, ok)
}

func TestTextCriterionRegexInvalid(t *testing.T) {
	criterion := &TextCriterion{
		MatchStrategy: TextMatchStrategyRegex,
	}
	ok, err := criterion.Match("source", "[invalid(")
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestTextCriterionUnknownStrategy(t *testing.T) {
	criterion := &TextCriterion{
		MatchStrategy: TextMatchStrategy("unknown"),
	}
	ok, err := criterion.Match("a", "b")
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestTextCriterionAllBranches(t *testing.T) {
	customCalled := false
	custom := &TextCriterion{
		Compare: func(actual, expected string) (bool, error) {
			customCalled = true
			return true, nil
		},
	}
	ok, err := custom.Match("x", "y")
	assert.True(t, ok)
	assert.NoError(t, err)
	assert.True(t, customCalled)

	exact := &TextCriterion{
		MatchStrategy: TextMatchStrategyExact,
	}
	ok, err = exact.Match("same", "same")
	assert.True(t, ok)
	assert.NoError(t, err)
	ok, err = exact.Match("same", "diff")
	assert.False(t, ok)
	assert.Error(t, err)

	contains := &TextCriterion{
		MatchStrategy: TextMatchStrategyContains,
	}
	ok, err = contains.Match("hello", "missing")
	assert.False(t, ok)
	assert.Error(t, err)

	regex := &TextCriterion{
		MatchStrategy: TextMatchStrategyRegex,
	}
	ok, err = regex.Match("abc123", "abc[0-9]+")
	assert.True(t, ok)
	assert.NoError(t, err)
	ok, err = regex.Match("xyz", "abc[0-9]+")
	assert.False(t, ok)
	assert.Error(t, err)
}
