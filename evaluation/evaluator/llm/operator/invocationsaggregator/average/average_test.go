//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package average

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestAggregateInvocationsAveragesScores(t *testing.T) {
	agg := New()
	ctx := context.Background()
	evalMetric := &metric.EvalMetric{Threshold: 0.75}
	result, err := agg.AggregateInvocations(ctx, []*evaluator.PerInvocationResult{
		{Score: 1, Status: status.EvalStatusPassed},
		{Score: 0, Status: status.EvalStatusFailed},
		{Score: 0, Status: status.EvalStatusNotEvaluated},
	}, evalMetric)
	require.NoError(t, err)
	assert.InDelta(t, 0.5, result.OverallScore, 1e-9)
	assert.Equal(t, status.EvalStatusFailed, result.OverallStatus)
	assert.Len(t, result.PerInvocationResults, 3)

	result, err = agg.AggregateInvocations(ctx, []*evaluator.PerInvocationResult{
		{Score: 0.8, Status: status.EvalStatusPassed},
		{Score: 0.9, Status: status.EvalStatusPassed},
	}, &metric.EvalMetric{Threshold: 0.5})
	require.NoError(t, err)
	assert.InDelta(t, 0.85, result.OverallScore, 1e-9)
	assert.Equal(t, status.EvalStatusPassed, result.OverallStatus)
}

func TestAggregateInvocationsNotEvaluated(t *testing.T) {
	agg := New()
	result, err := agg.AggregateInvocations(context.Background(), []*evaluator.PerInvocationResult{
		{Score: 0, Status: status.EvalStatusNotEvaluated},
	}, &metric.EvalMetric{Threshold: 0.6})
	require.NoError(t, err)
	assert.Equal(t, status.EvalStatusNotEvaluated, result.OverallStatus)
	assert.Equal(t, 0.0, result.OverallScore)
}
