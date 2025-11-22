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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

func TestTextCriterionMatchStrategies(t *testing.T) {
	criterion := &text.TextCriterion{
		CaseInsensitive: true,
		MatchStrategy:   text.TextMatchStrategyContains,
	}
	err := Match(criterion, "Hello World", "hello")
	assert.NoError(t, err)
}

func TestTextCriterionIgnore(t *testing.T) {
	criterion := &text.TextCriterion{
		Ignore: true,
	}
	err := Match(criterion, "anything", "value")
	assert.NoError(t, err)
}

func TestTextCriterionRegexInvalid(t *testing.T) {
	criterion := &text.TextCriterion{
		MatchStrategy: text.TextMatchStrategyRegex,
	}
	err := Match(criterion, "source", "[invalid(")
	assert.Error(t, err)
}

func TestTextCriterionUnknownStrategy(t *testing.T) {
	criterion := &text.TextCriterion{
		MatchStrategy: text.TextMatchStrategy("unknown"),
	}
	err := Match(criterion, "a", "b")
	assert.Error(t, err)
}

func TestTextCriterionAllBranches(t *testing.T) {
	customCalled := false
	custom := &text.TextCriterion{
		Compare: func(actual, expected string) error {
			customCalled = true
			return nil
		},
	}
	err := Match(custom, "x", "y")
	assert.NoError(t, err)
	assert.True(t, customCalled)

	exact := &text.TextCriterion{
		MatchStrategy: text.TextMatchStrategyExact,
	}
	err = Match(exact, "same", "same")
	assert.NoError(t, err)
	err = Match(exact, "same", "diff")
	assert.Error(t, err)

	contains := &text.TextCriterion{
		MatchStrategy: text.TextMatchStrategyContains,
	}
	err = Match(contains, "hello", "missing")
	assert.Error(t, err)

	regex := &text.TextCriterion{
		MatchStrategy: text.TextMatchStrategyRegex,
	}
	err = Match(regex, "abc123", "abc[0-9]+")
	assert.NoError(t, err)
	err = Match(regex, "xyz", "abc[0-9]+")
	assert.Error(t, err)
}
