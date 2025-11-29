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
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

func TestConfigJSONRoundTrip(t *testing.T) {
	cfg := &tooltrajectory.ToolTrajectoryCriterion{
		DefaultStrategy: &tooltrajectory.ToolTrajectoryStrategy{
			Name:      &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
			Arguments: &criterionjson.JSONCriterion{MatchStrategy: criterionjson.JSONMatchStrategyExact},
			Response:  &criterionjson.JSONCriterion{MatchStrategy: criterionjson.JSONMatchStrategyExact},
		},
		ToolStrategy: map[string]*tooltrajectory.ToolTrajectoryStrategy{
			"custom": {
				Name: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyRegex},
			},
		},
		OrderInsensitive: true,
	}
	data, err := json.Marshal(cfg)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"orderInsensitive":true`)
	assert.Contains(t, string(data), `"custom"`)

	var decoded tooltrajectory.ToolTrajectoryCriterion
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.True(t, decoded.OrderInsensitive)
	assert.NotNil(t, decoded.DefaultStrategy)
	assert.NotNil(t, decoded.ToolStrategy["custom"])
	assert.Equal(t, text.TextMatchStrategyRegex, decoded.ToolStrategy["custom"].Name.MatchStrategy)
}
