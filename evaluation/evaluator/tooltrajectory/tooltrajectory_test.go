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
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	criterionpkg "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
	ctooltrajectory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestConfigJSONRoundTrip(t *testing.T) {
	cfg := &tooltrajectory.ToolTrajectoryCriterion{
		DefaultStrategy: &tooltrajectory.ToolTrajectoryStrategy{
			Name:      &text.TextCriterion{MatchStrategy: text.TextMatchStrategyExact},
			Arguments: &criterionjson.JSONCriterion{MatchStrategy: criterionjson.JSONMatchStrategyExact},
			Result:    &criterionjson.JSONCriterion{MatchStrategy: criterionjson.JSONMatchStrategyExact},
		},
		ToolStrategy: map[string]*tooltrajectory.ToolTrajectoryStrategy{
			"custom": {
				Name: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyRegex},
			},
		},
		OrderSensitive: true,
	}
	data, err := json.Marshal(cfg)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"orderSensitive":true`)
	assert.Contains(t, string(data), `"custom"`)

	var decoded tooltrajectory.ToolTrajectoryCriterion
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.True(t, decoded.OrderSensitive)
	assert.NotNil(t, decoded.DefaultStrategy)
	assert.NotNil(t, decoded.ToolStrategy["custom"])
	assert.Equal(t, text.TextMatchStrategyRegex, decoded.ToolStrategy["custom"].Name.MatchStrategy)
}

func TestToolTrajectoryEvaluateSuccessAndFailure(t *testing.T) {
	e := New()
	actual := []*evalset.Invocation{{Tools: []*evalset.Tool{{ID: "1"}}}}
	expected := []*evalset.Invocation{{Tools: []*evalset.Tool{{ID: "1"}}}}
	criterion := &ctooltrajectory.ToolTrajectoryCriterion{
		Compare: func(actual, expected *evalset.Invocation) (bool, error) {
			if actual.InvocationID == "bad" {
				return false, assert.AnError
			}
			return true, nil
		},
	}
	metricConfig := &metric.EvalMetric{
		Threshold: 0.5,
		Criterion: &criterionpkg.Criterion{ToolTrajectory: criterion},
	}

	result, err := e.Evaluate(context.Background(), actual, expected, metricConfig)
	assert.NoError(t, err)
	assert.Equal(t, status.EvalStatusPassed, result.OverallStatus)
	assert.Len(t, result.PerInvocationResults, 1)
	assert.Equal(t, float64(1), result.PerInvocationResults[0].Score)

	// error branch from Compare.
	actual[0].InvocationID = "bad"
	result, err = e.Evaluate(context.Background(), actual, expected, metricConfig)
	assert.NoError(t, err)
	assert.Equal(t, status.EvalStatusFailed, result.OverallStatus)
	assert.Contains(t, result.PerInvocationResults[0].Details.Reason, assert.AnError.Error())
}

func TestToolTrajectoryEvaluateEdgeCases(t *testing.T) {
	e := New()
	criterion := &ctooltrajectory.ToolTrajectoryCriterion{}
	metricConfig := &metric.EvalMetric{
		Threshold: 0.1,
		Criterion: &criterionpkg.Criterion{ToolTrajectory: criterion},
	}

	// Empty input should return not evaluated.
	result, err := e.Evaluate(context.Background(), nil, nil, metricConfig)
	assert.NoError(t, err)
	assert.Equal(t, status.EvalStatusNotEvaluated, result.OverallStatus)

	// Length mismatch.
	_, err = e.Evaluate(context.Background(),
		[]*evalset.Invocation{{}}, []*evalset.Invocation{}, metricConfig)
	assert.Error(t, err)

	// Missing criterion.
	_, err = e.Evaluate(context.Background(),
		[]*evalset.Invocation{{}}, []*evalset.Invocation{{}}, &metric.EvalMetric{})
	assert.Error(t, err)
}
