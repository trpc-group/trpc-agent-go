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
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionpkg "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
	ctooltrajectory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestToolTrajectoryEvaluator_EvaluateSuccessAndFailure(t *testing.T) {
	ev := New()
	ttCriterion := &ctooltrajectory.ToolTrajectoryCriterion{
		Compare: func(actual, expected *evalset.Invocation) (bool, error) {
			return actual.InvocationID == expected.InvocationID, nil
		},
	}
	evalMetric := &metric.EvalMetric{Threshold: 0.5, Criterion: &criterion.Criterion{ToolTrajectory: ttCriterion}}

	actuals := []*evalset.Invocation{{InvocationID: "a"}}
	expecteds := []*evalset.Invocation{{InvocationID: "a"}}
	result, err := ev.Evaluate(context.Background(), actuals, expecteds, evalMetric)
	require.NoError(t, err)
	require.Len(t, result.PerInvocationResults, 1)
	assert.Equal(t, 1.0, result.OverallScore)
	assert.Equal(t, status.EvalStatusPassed, result.OverallStatus)

	expecteds[0].InvocationID = "b"
	result, err = ev.Evaluate(context.Background(), actuals, expecteds, evalMetric)
	require.NoError(t, err)
	require.Len(t, result.PerInvocationResults, 1)
	assert.Equal(t, 0.0, result.PerInvocationResults[0].Score)
	assert.Equal(t, status.EvalStatusFailed, result.PerInvocationResults[0].Status)
	assert.Equal(t, status.EvalStatusFailed, result.OverallStatus)
}

func TestToolTrajectoryEvaluator_Errors(t *testing.T) {
	ev := New()
	_, err := ev.Evaluate(context.Background(), nil, nil, nil)
	require.Error(t, err)

	evalMetric := &metric.EvalMetric{Threshold: 0.5, Criterion: &criterion.Criterion{ToolTrajectory: &ctooltrajectory.ToolTrajectoryCriterion{}}}
	_, err = ev.Evaluate(context.Background(), []*evalset.Invocation{{}}, []*evalset.Invocation{}, evalMetric)
	require.Error(t, err)
}

func TestToolTrajectoryEvaluator_ErrorReason(t *testing.T) {
	ev := New()
	ttCriterion := &ctooltrajectory.ToolTrajectoryCriterion{
		Compare: func(actual, expected *evalset.Invocation) (bool, error) {
			return false, assert.AnError
		},
	}
	evalMetric := &metric.EvalMetric{Threshold: 0.5, Criterion: &criterion.Criterion{ToolTrajectory: ttCriterion}}
	result, err := ev.Evaluate(context.Background(), []*evalset.Invocation{{InvocationID: "a"}}, []*evalset.Invocation{{InvocationID: "a"}}, evalMetric)
	require.NoError(t, err)
	require.Len(t, result.PerInvocationResults, 1)
	assert.Equal(t, status.EvalStatusFailed, result.OverallStatus)
	assert.Contains(t, result.PerInvocationResults[0].Details.Reason, "tool trajectory mismatch")
}

func TestToolTrajectoryEvaluator_NoInvocations(t *testing.T) {
	ev := New()
	ttCriterion := &ctooltrajectory.ToolTrajectoryCriterion{
		Compare: func(actual, expected *evalset.Invocation) (bool, error) {
			return true, nil
		},
	}
	evalMetric := &metric.EvalMetric{Threshold: 0.5, Criterion: &criterion.Criterion{ToolTrajectory: ttCriterion}}
	result, err := ev.Evaluate(context.Background(), []*evalset.Invocation{}, []*evalset.Invocation{}, evalMetric)
	require.NoError(t, err)
	assert.Equal(t, status.EvalStatusNotEvaluated, result.OverallStatus)
	assert.Equal(t, 0.0, result.OverallScore)
	assert.Empty(t, result.PerInvocationResults)
}

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
