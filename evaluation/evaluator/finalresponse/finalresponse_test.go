//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package finalresponse

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	cfinalresponse "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/finalresponse"
	criterionrouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TestFinalResponseEvaluator_EvaluateSuccessAndFailure verifies the overall status and score aggregation.
func TestFinalResponseEvaluator_EvaluateSuccessAndFailure(t *testing.T) {
	ev := New()
	frCriterion := &cfinalresponse.FinalResponseCriterion{
		Compare: func(actual, expected *evalset.Invocation) (bool, error) {
			return actual.InvocationID == expected.InvocationID, nil
		},
	}
	evalMetric := &metric.EvalMetric{Threshold: 0.5, Criterion: &criterion.Criterion{FinalResponse: frCriterion}}

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

// TestFinalResponseEvaluator_Errors verifies input validation and error cases.
func TestFinalResponseEvaluator_Errors(t *testing.T) {
	ev := New()
	_, err := ev.Evaluate(context.Background(), nil, nil, nil)
	require.Error(t, err)

	evalMetric := &metric.EvalMetric{Threshold: 0.5, Criterion: &criterion.Criterion{FinalResponse: &cfinalresponse.FinalResponseCriterion{}}}
	_, err = ev.Evaluate(context.Background(), []*evalset.Invocation{{}}, []*evalset.Invocation{}, evalMetric)
	require.Error(t, err)
}

// TestFinalResponseEvaluator_ErrorReason verifies that evaluation errors are propagated as a reason string.
func TestFinalResponseEvaluator_ErrorReason(t *testing.T) {
	ev := New()
	frCriterion := &cfinalresponse.FinalResponseCriterion{
		Compare: func(actual, expected *evalset.Invocation) (bool, error) {
			return false, assert.AnError
		},
	}
	evalMetric := &metric.EvalMetric{Threshold: 0.5, Criterion: &criterion.Criterion{FinalResponse: frCriterion}}
	result, err := ev.Evaluate(context.Background(), []*evalset.Invocation{{InvocationID: "a"}}, []*evalset.Invocation{{InvocationID: "a"}}, evalMetric)
	require.NoError(t, err)
	require.Len(t, result.PerInvocationResults, 1)
	assert.Equal(t, status.EvalStatusFailed, result.OverallStatus)
	assert.Contains(t, result.PerInvocationResults[0].Details.Reason, "final response mismatch")
}

// TestFinalResponseEvaluator_NoInvocations verifies behavior when there are no invocations to evaluate.
func TestFinalResponseEvaluator_NoInvocations(t *testing.T) {
	ev := New()
	evalMetric := &metric.EvalMetric{Threshold: 0.5, Criterion: &criterion.Criterion{FinalResponse: &cfinalresponse.FinalResponseCriterion{}}}
	result, err := ev.Evaluate(context.Background(), []*evalset.Invocation{}, []*evalset.Invocation{}, evalMetric)
	require.NoError(t, err)
	assert.Equal(t, status.EvalStatusNotEvaluated, result.OverallStatus)
	assert.Equal(t, 0.0, result.OverallScore)
	assert.Empty(t, result.PerInvocationResults)
}

// TestFinalResponseEvaluator_TextCriterionIntegration verifies deterministic text criterion integration.
func TestFinalResponseEvaluator_TextCriterionIntegration(t *testing.T) {
	ev := New()
	evalMetric := &metric.EvalMetric{
		Threshold: 1.0,
		Criterion: &criterion.Criterion{
			FinalResponse: &cfinalresponse.FinalResponseCriterion{
				Text: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyContains},
			},
		},
	}
	actuals := []*evalset.Invocation{{FinalResponse: &model.Message{Content: "hello world"}}}
	expecteds := []*evalset.Invocation{{FinalResponse: &model.Message{Content: "world"}}}
	result, err := ev.Evaluate(context.Background(), actuals, expecteds, evalMetric)
	require.NoError(t, err)
	assert.Equal(t, status.EvalStatusPassed, result.OverallStatus)
	assert.Equal(t, 1.0, result.OverallScore)
}

// TestFinalResponseEvaluator_RougeCriterionIntegration verifies ROUGE scoring integration and threshold checks.
func TestFinalResponseEvaluator_RougeCriterionIntegration(t *testing.T) {
	ev := New()
	evalMetric := &metric.EvalMetric{
		Threshold: 0.5,
		Criterion: &criterion.Criterion{
			FinalResponse: &cfinalresponse.FinalResponseCriterion{
				Rouge: &criterionrouge.RougeCriterion{
					RougeType: "rouge1",
					Measure:   criterionrouge.RougeMeasureF1,
					Threshold: criterionrouge.Score{Precision: 0.9, Recall: 0.3, F1: 0.5},
				},
			},
		},
	}
	actuals := []*evalset.Invocation{{FinalResponse: &model.Message{Content: "testing"}}}
	expecteds := []*evalset.Invocation{{FinalResponse: &model.Message{Content: "testing one two"}}}
	result, err := ev.Evaluate(context.Background(), actuals, expecteds, evalMetric)
	require.NoError(t, err)
	require.Len(t, result.PerInvocationResults, 1)
	assert.InDelta(t, 1.0, result.PerInvocationResults[0].Score, 1e-12)
	assert.Equal(t, status.EvalStatusPassed, result.PerInvocationResults[0].Status)
	assert.Equal(t, status.EvalStatusPassed, result.OverallStatus)
	assert.Empty(t, result.PerInvocationResults[0].Details.Reason)
}
