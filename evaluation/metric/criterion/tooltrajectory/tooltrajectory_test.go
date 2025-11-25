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
	"google.golang.org/genai"
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
			Response:  &criterionjson.JSONCriterion{},
		},
		ToolStrategy: map[string]*ToolTrajectoryStrategy{
			"foo": {
				Name: &text.TextCriterion{
					Ignore:          true,
					CaseInsensitive: true,
					MatchStrategy:   text.TextMatchStrategyContains,
				},
			},
		},
		OrderInsensitive: true,
	}
	data, err := json.Marshal(criterion)
	assert.NoError(t, err)
	assert.JSONEq(t, `{
		"defaultStrategy":{
			"name":{"ignore":true,"caseInsensitive":true,"matchStrategy":"exact"},
			"arguments":{},
			"response":{}
		},
		"toolStrategy":{
			"foo":{"name":{"ignore":true,"caseInsensitive":true,"matchStrategy":"contains"}}
		},
		"orderInsensitive":true
	}`, string(data))
	var decoded ToolTrajectoryCriterion
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.True(t, decoded.OrderInsensitive)
	assert.NotNil(t, decoded.DefaultStrategy)
	assert.Equal(t, text.TextMatchStrategyExact, decoded.DefaultStrategy.Name.MatchStrategy)
	assert.True(t, decoded.DefaultStrategy.Name.Ignore)
	assert.True(t, decoded.DefaultStrategy.Name.CaseInsensitive)
	assert.NotNil(t, decoded.ToolStrategy["foo"])
	assert.Equal(t, text.TextMatchStrategyContains, decoded.ToolStrategy["foo"].Name.MatchStrategy)
	assert.True(t, decoded.ToolStrategy["foo"].Name.Ignore)
	assert.True(t, decoded.ToolStrategy["foo"].Name.CaseInsensitive)
}

func TestToolTrajectoryCriterionJSONOmitEmpty(t *testing.T) {
	criterion := &ToolTrajectoryCriterion{}
	data, err := json.Marshal(criterion)
	assert.NoError(t, err)
	assert.JSONEq(t, `{}`, string(data))
}

func TestToolTrajectoryStrategyJSONRoundTrip(t *testing.T) {
	strategy := &ToolTrajectoryStrategy{
		Name: &text.TextCriterion{
			Ignore:          true,
			CaseInsensitive: true,
			MatchStrategy:   text.TextMatchStrategyExact,
		},
		Arguments: &criterionjson.JSONCriterion{},
		Response:  &criterionjson.JSONCriterion{},
	}
	data, err := json.Marshal(strategy)
	assert.NoError(t, err)
	assert.JSONEq(t, `{
		"name":{"ignore":true,"caseInsensitive":true,"matchStrategy":"exact"},
		"arguments":{},
		"response":{}
	}`, string(data))

	var decoded ToolTrajectoryStrategy
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, text.TextMatchStrategyExact, decoded.Name.MatchStrategy)
	assert.True(t, decoded.Name.Ignore)
	assert.True(t, decoded.Name.CaseInsensitive)
	assert.NotNil(t, decoded.Arguments)
	assert.NotNil(t, decoded.Response)
}

func TestToolTrajectoryCriterionMatchOrderInsensitive(t *testing.T) {
	actual := makeInvocation(
		[]toolData{
			{id: "call-1", name: "shared", args: map[string]any{"a": 1}, response: map[string]any{"r": 2}},
			{id: "call-2", name: "shared", args: map[string]any{"a": 2}, response: map[string]any{"r": 3}},
		},
	)
	expected := makeInvocation(
		[]toolData{
			{id: "call-2", name: "shared", args: map[string]any{"a": 2}, response: map[string]any{"r": 3}},
			{id: "call-1", name: "shared", args: map[string]any{"a": 1}, response: map[string]any{"r": 2}},
		},
	)

	criterion := &ToolTrajectoryCriterion{
		OrderInsensitive: true,
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestToolTrajectoryCriterionMissingResponse(t *testing.T) {
	actual := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "call-1", Name: "tool"},
			},
			ToolResponses: []*genai.FunctionResponse{},
		},
	}
	expected := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "call-1", Name: "tool"},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "call-1", Name: "tool"},
			},
		},
	}
	criterion := &ToolTrajectoryCriterion{}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionCustomStrategy(t *testing.T) {
	actual := makeInvocation(
		[]toolData{
			{id: "call-1", name: "custom", args: map[string]any{"k": "v"}, response: map[string]any{"r": "x"}},
		},
	)
	expected := makeInvocation(
		[]toolData{
			{id: "call-1", name: "custom", args: map[string]any{"k": "v"}, response: map[string]any{"r": "x"}},
		},
	)
	customStrategy := &ToolTrajectoryStrategy{
		Name: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
	}
	criterion := &ToolTrajectoryCriterion{
		ToolStrategy: map[string]*ToolTrajectoryStrategy{
			"custom": customStrategy,
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

type toolData struct {
	id       string
	name     string
	args     map[string]any
	response map[string]any
}

func makeInvocation(tools []toolData) *evalset.Invocation {
	toolUses := make([]*genai.FunctionCall, 0, len(tools))
	toolResponses := make([]*genai.FunctionResponse, 0, len(tools))
	for _, t := range tools {
		toolUses = append(toolUses, &genai.FunctionCall{
			ID:   t.id,
			Name: t.name,
			Args: t.args,
		})
		toolResponses = append(toolResponses, &genai.FunctionResponse{
			ID:       t.id,
			Name:     t.name,
			Response: t.response,
		})
	}
	return &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses:      toolUses,
			ToolResponses: toolResponses,
		},
	}
}

func TestToolTrajectoryCriterionIDMismatch(t *testing.T) {
	actual := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "use-1", Name: "tool"},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "resp-1", Name: "tool"},
			},
		},
	}
	expected := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "use-1", Name: "tool"},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "use-1", Name: "tool"},
			},
		},
	}
	criterion := New()
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionNilInvocation(t *testing.T) {
	criterion := New()
	ok, err := criterion.Match(nil, makeInvocation(nil))
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionNilIntermediate(t *testing.T) {
	criterion := New()
	ok, err := criterion.Match(
		&evalset.Invocation{},
		&evalset.Invocation{IntermediateData: &evalset.IntermediateData{}},
	)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionEmptyToolUseID(t *testing.T) {
	actual := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{Name: "tool"},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "resp-1", Name: "tool"},
			},
		},
	}
	expected := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "resp-1", Name: "tool"},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "resp-1", Name: "tool"},
			},
		},
	}
	ok, err := New().Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionDuplicateResponseID(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	actual.IntermediateData.ToolResponses = append(actual.IntermediateData.ToolResponses, &genai.FunctionResponse{
		ID:       "call-1",
		Name:     "tool",
		Response: map[string]any{"r": 2},
	})
	ok, err := New().Match(actual, makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	}))
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionCustomCompare(t *testing.T) {
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

func TestToolTrajectoryCriterionExpectedResponseCountMismatch(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	expected := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "call-1", Name: "tool", Args: map[string]any{"a": 1}},
			},
			ToolResponses: []*genai.FunctionResponse{},
		},
	}
	ok, err := New().Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionToolUsesCountMismatch(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
		{id: "call-2", name: "tool", args: map[string]any{"a": 2}, response: map[string]any{"r": 2}},
	})
	ok, err := New().Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionZeroTools(t *testing.T) {
	actual := &evalset.Invocation{IntermediateData: &evalset.IntermediateData{}}
	expected := &evalset.Invocation{IntermediateData: &evalset.IntermediateData{}}
	ok, err := New().Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestToolTrajectoryCriterionExpectedInvalidID(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	expected := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "", Name: "tool", Args: map[string]any{"a": 1}},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "call-1", Name: "tool", Response: map[string]any{"r": 1}},
			},
		},
	}
	ok, err := New().Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionStrategyMismatch(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool-A", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool-B", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	strategy := &ToolTrajectoryStrategy{
		Name: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
	}
	criterion := New(WithTool(map[string]*ToolTrajectoryStrategy{"tool-A": strategy}))
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionDuplicateToolUseID(t *testing.T) {
	actual := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "dup", Name: "tool"},
				{ID: "dup", Name: "tool"},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "dup", Name: "tool"},
				{ID: "dup2", Name: "tool"},
			},
		},
	}
	expected := makeInvocation([]toolData{
		{id: "dup", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
		{id: "dup2", name: "tool", args: map[string]any{"a": 2}, response: map[string]any{"r": 2}},
	})
	ok, err := New().Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionDuplicateToolResponseID(t *testing.T) {
	actual := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "call-1", Name: "tool"},
				{ID: "call-2", Name: "tool"},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "call-1", Name: "tool"},
				{ID: "call-1", Name: "tool"},
			},
		},
	}
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
		{id: "call-2", name: "tool", args: map[string]any{"a": 2}, response: map[string]any{"r": 2}},
	})
	ok, err := New().Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionMissingResponseID(t *testing.T) {
	actual := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "call-1", Name: "tool"},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "other", Name: "tool"},
			},
		},
	}
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	ok, err := New().Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolComparerOrderInsensitiveMarshalError(t *testing.T) {
	actual := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "call-1", Name: "tool", Args: map[string]any{"bad": make(chan int)}},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "call-1", Name: "tool", Response: map[string]any{"r": 1}},
			},
		},
	}
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{}, response: map[string]any{"r": 1}},
	})
	ok, err := New(WithOrderInsensitive(true)).Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolComparerOrderInsensitiveMarshalResponseError(t *testing.T) {
	actual := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "call-1", Name: "tool", Args: map[string]any{"a": 1}},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "call-1", Name: "tool", Response: map[string]any{"bad": make(chan int)}},
			},
		},
	}
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	ok, err := New(WithOrderInsensitive(true)).Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolComparerLessThanBranches(t *testing.T) {
	left := &toolComparer{name: "a", argsOrder: "1", responseOrder: "1"}
	right := &toolComparer{name: "b", argsOrder: "0", responseOrder: "0"}
	assert.True(t, left.lessThan(right))

	left2 := &toolComparer{name: "a", argsOrder: "2", responseOrder: "1"}
	right2 := &toolComparer{name: "a", argsOrder: "3", responseOrder: "0"}
	assert.True(t, left2.lessThan(right2))

	left3 := &toolComparer{name: "a", argsOrder: "1", responseOrder: "2"}
	right3 := &toolComparer{name: "a", argsOrder: "1", responseOrder: "3"}
	assert.True(t, left3.lessThan(right3))
}

func TestToolTrajectoryStrategyArgumentAndResponseMismatch(t *testing.T) {
	strategy := &ToolTrajectoryStrategy{
		Arguments: &criterionjson.JSONCriterion{},
		Response:  &criterionjson.JSONCriterion{},
	}
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 2}, response: map[string]any{"r": 3}},
	})
	criterion := New(WithTool(map[string]*ToolTrajectoryStrategy{
		"tool": strategy,
	}))
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestGetToolComparerNilInputs(t *testing.T) {
	_, err := getToolComparer(nil, &genai.FunctionResponse{}, false)
	assert.Error(t, err)
	_, err = getToolComparer(&genai.FunctionCall{}, nil, false)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionMissingResponseSet(t *testing.T) {
	actual := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "call-1", Name: "tool"},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "call-1", Name: "tool"},
			},
		},
	}
	expected := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "call-1", Name: "tool"},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "other", Name: "tool"},
			},
		},
	}
	ok, err := New().Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionFallbackDefault(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	criterion := &ToolTrajectoryCriterion{
		DefaultStrategy: nil,
		ToolStrategy:    nil,
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestToolTrajectoryCriterionFallbackDefaultStrategy(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	criterion := &ToolTrajectoryCriterion{
		DefaultStrategy: nil,
		ToolStrategy:    nil,
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestToolTrajectoryCriterionEmptyToolResponseID(t *testing.T) {
	actual := &evalset.Invocation{
		IntermediateData: &evalset.IntermediateData{
			ToolUses: []*genai.FunctionCall{
				{ID: "call-1", Name: "tool"},
			},
			ToolResponses: []*genai.FunctionResponse{
				{ID: "", Name: "tool"},
			},
		},
	}
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{}, response: map[string]any{}},
	})
	ok, err := New().Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolTrajectoryCriterionStrategyLookupByExpectedName(t *testing.T) {
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "unknown", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "custom", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	customStrategy := &ToolTrajectoryStrategy{}
	criterion := New(WithTool(map[string]*ToolTrajectoryStrategy{
		"custom": customStrategy,
	}))
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestToolTrajectoryStrategyResponseMismatchOnly(t *testing.T) {
	strategy := &ToolTrajectoryStrategy{
		Arguments: &criterionjson.JSONCriterion{},
		Response:  &criterionjson.JSONCriterion{},
	}
	actual := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 1}},
	})
	expected := makeInvocation([]toolData{
		{id: "call-1", name: "tool", args: map[string]any{"a": 1}, response: map[string]any{"r": 2}},
	})
	criterion := New(WithTool(map[string]*ToolTrajectoryStrategy{
		"tool": strategy,
	}))
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToolComparerLessThanEqual(t *testing.T) {
	left := &toolComparer{name: "same", argsOrder: "1", responseOrder: "1"}
	right := &toolComparer{name: "same", argsOrder: "1", responseOrder: "1"}
	assert.False(t, left.lessThan(right))
}

func TestInternalTextAndMapWrappers(t *testing.T) {
	txt := &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact}
	ok, err := txt.Match("same", "same")
	assert.True(t, ok)
	assert.NoError(t, err)

	crit := &criterionjson.JSONCriterion{}
	ok, err = crit.Match(map[string]any{"a": 1}, map[string]any{"a": 1})
	assert.True(t, ok)
	assert.NoError(t, err)
}
