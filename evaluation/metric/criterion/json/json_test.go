//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package json

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapCriterionCompareOverride(t *testing.T) {
	called := false
	criterion := &JSONCriterion{
		Compare: func(actual, expected map[string]any) (bool, error) {
			called = true
			return true, nil
		},
	}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "v"})
	assert.True(t, ok)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestMapCriterionDeepEqualMismatch(t *testing.T) {
	criterion := &JSONCriterion{}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "diff"})
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestMapCriterionDeepEqualSuccess(t *testing.T) {
	criterion := &JSONCriterion{}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "v"})
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionIgnoreSkipsCompare(t *testing.T) {
	called := false
	criterion := &JSONCriterion{
		Ignore: true,
		Compare: func(actual, expected map[string]any) (bool, error) {
			called = true
			return false, nil
		},
		MatchStrategy: JSONMatchStrategyExact,
	}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "diff"})
	assert.True(t, ok)
	assert.NoError(t, err)
	assert.False(t, called)
}

func TestJSONCriterionInvalidMatchStrategy(t *testing.T) {
	criterion := &JSONCriterion{
		MatchStrategy: JSONMatchStrategy("invalid"),
	}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "v"})
	assert.False(t, ok)
	assert.Error(t, err)
}
