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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/maptext"
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
			Arguments: &maptext.MapTextCriterion{},
			Response:  &maptext.MapTextCriterion{},
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
		Arguments: &maptext.MapTextCriterion{
			TextCriterion: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyRegex},
		},
		Response: &maptext.MapTextCriterion{
			TextCriterion: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyContains},
		},
	}
	data, err := json.Marshal(strategy)
	assert.NoError(t, err)
	assert.JSONEq(t, `{
		"name":{"ignore":true,"caseInsensitive":true,"matchStrategy":"exact"},
		"arguments":{"textCriterion":{"matchStrategy":"regex"}},
		"response":{"textCriterion":{"matchStrategy":"contains"}}
	}`, string(data))

	var decoded ToolTrajectoryStrategy
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, text.TextMatchStrategyExact, decoded.Name.MatchStrategy)
	assert.True(t, decoded.Name.Ignore)
	assert.True(t, decoded.Name.CaseInsensitive)
	assert.NotNil(t, decoded.Arguments)
	assert.NotNil(t, decoded.Response)
	assert.Equal(t, text.TextMatchStrategyRegex, decoded.Arguments.TextCriterion.MatchStrategy)
	assert.Equal(t, text.TextMatchStrategyContains, decoded.Response.TextCriterion.MatchStrategy)
}
