//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package majorityvote

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestAggregateSamplesMajorityPositive(t *testing.T) {
	agg := New()
	ctx := context.Background()
	evalMetric := &metric.EvalMetric{Threshold: 0.5}
	positive := &evaluator.PerInvocationResult{Score: 0.7, Status: status.EvalStatusPassed}
	negative := &evaluator.PerInvocationResult{Score: 0.2, Status: status.EvalStatusFailed}

	result, err := agg.AggregateSamples(ctx, []*evaluator.PerInvocationResult{positive, negative, positive}, evalMetric)
	require.NoError(t, err)
	assert.Equal(t, positive, result)

	result, err = agg.AggregateSamples(ctx, []*evaluator.PerInvocationResult{negative, negative, positive}, evalMetric)
	require.NoError(t, err)
	assert.Equal(t, negative, result)
}

func TestAggregateSamplesHandlesEdgeCases(t *testing.T) {
	agg := New()
	evalMetric := &metric.EvalMetric{Threshold: 0.5}

	_, err := agg.AggregateSamples(context.Background(), nil, evalMetric)
	require.Error(t, err)

	ne := &evaluator.PerInvocationResult{Score: 0, Status: status.EvalStatusNotEvaluated}
	result, err := agg.AggregateSamples(context.Background(), []*evaluator.PerInvocationResult{ne}, evalMetric)
	require.NoError(t, err)
	assert.Equal(t, ne, result)
}
