//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package maptext

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/maptext"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

func TestMapTextCriterionCompareOverride(t *testing.T) {
	called := false
	criterion := &maptext.MapTextCriterion{
		Compare: func(actual, expected map[string]any) error {
			called = true
			return nil
		},
	}
	err := Match(criterion, map[string]any{"k": "v"}, map[string]any{"k": "v"})
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestMapTextCriterionTextMatch(t *testing.T) {
	criterion := &maptext.MapTextCriterion{
		TextCriterion: &text.TextCriterion{
			CaseInsensitive: true,
			MatchStrategy:   text.TextMatchStrategyExact,
		},
	}
	err := Match(criterion, map[string]any{"msg": "Hello"}, map[string]any{"msg": "hello"})
	assert.NoError(t, err)
}

func TestMapTextCriterionDeepEqualMismatch(t *testing.T) {
	criterion := &maptext.MapTextCriterion{}
	err := Match(criterion, map[string]any{"k": "v"}, map[string]any{"k": "diff"})
	assert.Error(t, err)
}

func TestMapTextCriterionMarshalErrors(t *testing.T) {
	criterion := &maptext.MapTextCriterion{
		TextCriterion: &text.TextCriterion{},
	}
	// Actual marshal error.
	actualErr := Match(criterion, map[string]any{"bad": make(chan int)}, map[string]any{"k": "v"})
	assert.Error(t, actualErr)
	// Expected marshal error.
	expectedErr := Match(criterion, map[string]any{"k": "v"}, map[string]any{"bad": make(chan int)})
	assert.Error(t, expectedErr)
}

func TestMapTextCriterionDeepEqualSuccess(t *testing.T) {
	criterion := &maptext.MapTextCriterion{}
	err := Match(criterion, map[string]any{"k": "v"}, map[string]any{"k": "v"})
	assert.NoError(t, err)
}
