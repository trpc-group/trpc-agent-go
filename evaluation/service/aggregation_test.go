//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestDefaultEvalCaseResultAggregator(t *testing.T) {
	tests := []struct {
		name       string
		metrics    []*evalresult.EvalMetricResult
		wantScore  float64
		wantStatus status.EvalStatus
	}{
		{name: "all passed", metrics: []*evalresult.EvalMetricResult{{EvalStatus: status.EvalStatusPassed}}, wantScore: 1, wantStatus: status.EvalStatusPassed},
		{name: "one failed", metrics: []*evalresult.EvalMetricResult{{EvalStatus: status.EvalStatusPassed}, {EvalStatus: status.EvalStatusFailed}}, wantScore: 0, wantStatus: status.EvalStatusFailed},
		{name: "not evaluated", metrics: []*evalresult.EvalMetricResult{{EvalStatus: status.EvalStatusNotEvaluated}}, wantScore: 0, wantStatus: status.EvalStatusNotEvaluated},
		{name: "empty metrics", metrics: []*evalresult.EvalMetricResult{}, wantScore: 0, wantStatus: status.EvalStatusNotEvaluated},
	}
	aggregator := defaultEvalCaseResultAggregator{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := aggregator.Aggregate(context.Background(), &EvalCaseResultAggregationInput{MetricResults: tc.metrics})
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tc.wantScore, got.Score)
			assert.Equal(t, tc.wantStatus, got.Status)
		})
	}
}

func TestDefaultEvalCaseResultAggregatorRejectsInvalidInput(t *testing.T) {
	aggregator := defaultEvalCaseResultAggregator{}
	got, err := aggregator.Aggregate(context.Background(), nil)
	assert.ErrorContains(t, err, "eval case result aggregation input is nil")
	assert.Nil(t, got)
	got, err = aggregator.Aggregate(context.Background(), &EvalCaseResultAggregationInput{
		MetricResults: []*evalresult.EvalMetricResult{{EvalStatus: status.EvalStatusUnknown}},
	})
	assert.ErrorContains(t, err, "summarize metric results")
	assert.Nil(t, got)
}
