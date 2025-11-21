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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTextCriterionMatchStrategies(t *testing.T) {
	criterion := &TextCriterion{
		CaseInsensitive: true,
		MatchStrategy:   TextMatchStrategyContains,
	}
	err := criterion.Match("Hello World", "hello")
	assert.NoError(t, err)
}

func TestTextCriterionIgnore(t *testing.T) {
	criterion := &TextCriterion{
		Ignore: true,
	}
	err := criterion.Match("anything", "value")
	assert.NoError(t, err)
}

func TestTextCriterionRegexInvalid(t *testing.T) {
	criterion := &TextCriterion{
		MatchStrategy: TextMatchStrategyRegex,
	}
	err := criterion.Match("source", "[invalid(")
	assert.Error(t, err)
}

func TestTextCriterionUnknownStrategy(t *testing.T) {
	criterion := &TextCriterion{
		MatchStrategy: TextMatchStrategy("unknown"),
	}
	err := criterion.Match("a", "b")
	assert.Error(t, err)
}

func TestTextCriterionAllBranches(t *testing.T) {
	customCalled := false
	custom := &TextCriterion{
		Compare: func(actual, expected string) error {
			customCalled = true
			return nil
		},
	}
	err := custom.Match("x", "y")
	assert.NoError(t, err)
	assert.True(t, customCalled)

	exact := &TextCriterion{
		MatchStrategy: TextMatchStrategyExact,
	}
	err = exact.Match("same", "same")
	assert.NoError(t, err)
	err = exact.Match("same", "diff")
	assert.Error(t, err)

	contains := &TextCriterion{
		MatchStrategy: TextMatchStrategyContains,
	}
	err = contains.Match("hello", "missing")
	assert.Error(t, err)

	regex := &TextCriterion{
		MatchStrategy: TextMatchStrategyRegex,
	}
	err = regex.Match("abc123", "abc[0-9]+")
	assert.NoError(t, err)
	err = regex.Match("xyz", "abc[0-9]+")
	assert.Error(t, err)
}
