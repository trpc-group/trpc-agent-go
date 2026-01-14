//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tooltrajectory

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

func TestToolTrajectoryCriterionJSONRoundTrip(t *testing.T) {
	criterion := &ToolTrajectoryCriterion{
		DefaultStrategy: &ToolTrajectoryStrategy{
			Name: &text.TextCriterion{
				Ignore:          true,
				CaseInsensitive: true,
				MatchStrategy:   text.TextMatchStrategyExact,
			},
			Arguments: &criterionjson.JSONCriterion{},
			Result:    &criterionjson.JSONCriterion{},
		},
		ToolStrategy: map[string]*ToolTrajectoryStrategy{
			"foo": {
				Name: &text.TextCriterion{
					Ignore:        true,
					MatchStrategy: text.TextMatchStrategyContains,
				},
			},
		},
		OrderSensitive: true,
		SubsetMatching: true,
	}
	data, err := json.Marshal(criterion)
	assert.NoError(t, err)
	assert.JSONEq(t, `{
		"defaultStrategy":{
			"name":{"ignore":true,"caseInsensitive":true,"matchStrategy":"exact"},
			"arguments":{},
			"result":{}
		},
		"toolStrategy":{
			"foo":{"name":{"ignore":true,"matchStrategy":"contains"}}
		},
		"orderSensitive":true,
		"subsetMatching":true
	}`, string(data))

	var decoded ToolTrajectoryCriterion
	assert.NoError(t, json.Unmarshal(data, &decoded))
	assert.True(t, decoded.OrderSensitive)
	assert.True(t, decoded.SubsetMatching)
	assert.Equal(t, text.TextMatchStrategyExact, decoded.DefaultStrategy.Name.MatchStrategy)
	assert.True(t, decoded.DefaultStrategy.Name.Ignore)
	assert.True(t, decoded.DefaultStrategy.Name.CaseInsensitive)
	assert.Equal(t, text.TextMatchStrategyContains, decoded.ToolStrategy["foo"].Name.MatchStrategy)
	assert.True(t, decoded.ToolStrategy["foo"].Name.Ignore)
}

func TestToolTrajectoryStrategyJSONRoundTrip(t *testing.T) {
	strategy := &ToolTrajectoryStrategy{
		Name: &text.TextCriterion{
			Ignore:          true,
			CaseInsensitive: true,
			MatchStrategy:   text.TextMatchStrategyExact,
		},
		Arguments: &criterionjson.JSONCriterion{},
		Result:    &criterionjson.JSONCriterion{},
	}
	data, err := json.Marshal(strategy)
	assert.NoError(t, err)
	assert.JSONEq(t, `{
		"name":{"ignore":true,"caseInsensitive":true,"matchStrategy":"exact"},
		"arguments":{},
		"result":{}
	}`, string(data))

	var decoded ToolTrajectoryStrategy
	assert.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, text.TextMatchStrategyExact, decoded.Name.MatchStrategy)
	assert.True(t, decoded.Name.Ignore)
	assert.True(t, decoded.Name.CaseInsensitive)
	assert.NotNil(t, decoded.Arguments)
	assert.NotNil(t, decoded.Result)
}

func TestToolTrajectoryCriterionMatchUnordered(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "shared", args: map[string]any{"a": 1}, result: map[string]any{"r": 2}},
		{id: "call-2", name: "shared", args: map[string]any{"a": 2}, result: map[string]any{"r": 3}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-2", name: "shared", args: map[string]any{"a": 2}, result: map[string]any{"r": 3}},
		{id: "call-1", name: "shared", args: map[string]any{"a": 1}, result: map[string]any{"r": 2}},
	})

	ok, err := New().Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestToolTrajectoryCriterionOrderSensitiveMismatch(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}},
		{id: "call-2", name: "tool", args: map[string]any{"a": 2}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-2", name: "tool", args: map[string]any{"a": 2}},
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}},
	})

	ok, err := New(WithOrderSensitive(true)).Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionOrderSensitiveLeadingExtraActual(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-0", name: "other", args: map[string]any{"extra": 1}},
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}},
		{id: "call-2", name: "tool", args: map[string]any{"a": 2}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}},
		{id: "call-2", name: "tool", args: map[string]any{"a": 2}},
		{id: "call-3", name: "tool", args: map[string]any{"a": 3}},
	})

	ok, err := New(WithOrderSensitive(true)).Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tool id call-3")
}

func TestToolTrajectoryCriterionSubsetMatching(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 0}},
		{id: "call-2", name: "tool", args: map[string]any{"a": 1}},
		{id: "call-3", name: "tool", args: map[string]any{"a": 2}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-2", name: "tool", args: map[string]any{"a": 1}},
		{id: "call-3", name: "tool", args: map[string]any{"a": 2}},
	})

	ok, err := New(WithSubsetMatching(true)).Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestToolTrajectoryCriterionCountMismatch(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}},
		{id: "call-2", name: "tool", args: map[string]any{"a": 2}},
	})

	ok, err := New().Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionSubsetTooFewActual(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}},
		{id: "call-2", name: "tool", args: map[string]any{"a": 2}},
	})
	ok, err := New(WithSubsetMatching(true)).Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "actual(1) < expected(2)")
}

func TestToolTrajectoryCriterionStrategyMismatch(t *testing.T) {
	strategy := &ToolTrajectoryStrategy{
		Name:      &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
		Arguments: &criterionjson.JSONCriterion{},
		Result:    &criterionjson.JSONCriterion{},
	}
	criterion := New(WithTool(map[string]*ToolTrajectoryStrategy{
		"tool": strategy,
	}))
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"k": "v"}, result: map[string]any{"out": "ok"}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"k": "other"}, result: map[string]any{"out": "ok"}},
	})

	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionNilInvocation(t *testing.T) {
	ok, err := New().Match(nil, makeInvocation(nil))
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionCompareOverride(t *testing.T) {
	var called bool
	criterion := &ToolTrajectoryCriterion{
		Compare: func(actual, expected *evalset.Invocation) (bool, error) {
			called = true
			return true, nil
		},
	}
	ok, err := criterion.Match(&evalset.Invocation{}, &evalset.Invocation{})
	assert.True(t, ok)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestToolTrajectoryCriterionStrategyLookupByExpectedName(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "unknown", args: map[string]any{}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "custom", args: map[string]any{}},
	})
	criterion := New(WithTool(map[string]*ToolTrajectoryStrategy{
		"custom": {},
	}))
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestToolTrajectoryCriterionZeroTools(t *testing.T) {
	ok, err := New().Match(&evalset.Invocation{}, &evalset.Invocation{})
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestToolTrajectoryCriterionOrderedMatchSkipAndSucceed(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "other", args: map[string]any{"a": 0}},
		{id: "call-2", name: "tool", args: map[string]any{"a": 1}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-2", name: "tool", args: map[string]any{"a": 1}},
	})
	ok, err := New(WithOrderSensitive(true), WithSubsetMatching(true)).Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestToolTrajectoryMatchToolNil(t *testing.T) {
	criterion := New()
	err := criterion.matchTool(nil, &evalset.Tool{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "actual or expected tool is nil")
}

func TestToolTrajectoryMatchToolMismatch(t *testing.T) {
	criterion := New()
	err := criterion.matchTool(&evalset.Tool{ID: "a", Name: "one"}, &evalset.Tool{ID: "b", Name: "two"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mismatch with expected tool")

	// Mismatch from strategy false without error.
	criterion = New(WithTool(map[string]*ToolTrajectoryStrategy{
		"one": {
			Name: &text.TextCriterion{
				Compare: func(_, _ string) (bool, error) {
					return false, nil
				},
			},
		},
	}))
	err = criterion.matchTool(&evalset.Tool{ID: "a", Name: "one"}, &evalset.Tool{ID: "a", Name: "one"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mismatch with expected tool")
}

func TestToolTrajectoryGetStrategyDefault(t *testing.T) {
	criterion := &ToolTrajectoryCriterion{}
	strategy := criterion.getStrategy(&evalset.Tool{Name: "x"}, &evalset.Tool{Name: "y"})
	assert.Equal(t, defaultToolTrajectoryStrategy, strategy)
}

func TestToolTrajectoryStrategyMatchBranches(t *testing.T) {
	// Name mismatch with error.
	strategy := &ToolTrajectoryStrategy{
		Name: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
	}
	ok, err := strategy.Match(&evalset.Tool{Name: "a"}, &evalset.Tool{Name: "b"})
	assert.False(t, ok)
	assert.Error(t, err)

	// Name mismatch without error using Compare.
	strategy = &ToolTrajectoryStrategy{
		Name: &text.TextCriterion{
			Compare: func(actual, expected string) (bool, error) {
				return false, nil
			},
		},
	}
	ok, err = strategy.Match(&evalset.Tool{Name: "a"}, &evalset.Tool{Name: "a"})
	assert.False(t, ok)
	assert.Error(t, err)

	// Arguments mismatch.
	strategy = &ToolTrajectoryStrategy{
		Arguments: &criterionjson.JSONCriterion{},
	}
	ok, err = strategy.Match(
		&evalset.Tool{Name: "a", Arguments: map[string]any{"k": 1}},
		&evalset.Tool{Name: "a", Arguments: map[string]any{"k": 2}},
	)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "arguments mismatch")

	// Arguments array mismatch.
	ok, err = strategy.Match(
		&evalset.Tool{Name: "a", Arguments: []any{float64(1)}},
		&evalset.Tool{Name: "a", Arguments: []any{float64(2)}},
	)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "arguments mismatch")

	// Arguments array match.
	ok, err = strategy.Match(
		&evalset.Tool{Name: "a", Arguments: []any{float64(1), float64(2)}},
		&evalset.Tool{Name: "a", Arguments: []any{float64(1), float64(2)}},
	)
	assert.True(t, ok)
	assert.NoError(t, err)

	// Result mismatch.
	strategy = &ToolTrajectoryStrategy{
		Result: &criterionjson.JSONCriterion{},
	}
	ok, err = strategy.Match(
		&evalset.Tool{Name: "a", Result: map[string]any{"k": 1}},
		&evalset.Tool{Name: "a", Result: map[string]any{"k": 2}},
	)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "result mismatch")

	// Result array mismatch.
	ok, err = strategy.Match(
		&evalset.Tool{Name: "a", Result: []any{float64(1)}},
		&evalset.Tool{Name: "a", Result: []any{float64(2)}},
	)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "result mismatch")

	// Result array match.
	ok, err = strategy.Match(
		&evalset.Tool{Name: "a", Result: []any{float64(1), float64(2)}},
		&evalset.Tool{Name: "a", Result: []any{float64(1), float64(2)}},
	)
	assert.True(t, ok)
	assert.NoError(t, err)

	// Success path.
	strategy = &ToolTrajectoryStrategy{}
	ok, err = strategy.Match(&evalset.Tool{Name: "a"}, &evalset.Tool{Name: "a"})
	assert.True(t, ok)
	assert.NoError(t, err)
}

type toolData struct {
	id     string
	name   string
	args   map[string]any
	result map[string]any
}

func makeInvocation(tools []toolData) *evalset.Invocation {
	inv := &evalset.Invocation{
		Tools: make([]*evalset.Tool, 0, len(tools)),
	}
	for _, t := range tools {
		inv.Tools = append(inv.Tools, &evalset.Tool{
			ID:        t.id,
			Name:      t.name,
			Arguments: t.args,
			Result:    t.result,
		})
	}
	return inv
}
