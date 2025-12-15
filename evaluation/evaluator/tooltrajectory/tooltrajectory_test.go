package tooltrajectory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
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
