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
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	istatus "trpc.group/trpc-go/trpc-agent-go/evaluation/internal/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// EvalCaseResultAggregator aggregates metric results into one eval case result.
type EvalCaseResultAggregator interface {
	// Aggregate computes the case-level score and status from metric results.
	Aggregate(ctx context.Context, input *EvalCaseResultAggregationInput) (*EvalCaseResultAggregationResult, error)
}

// EvalCaseResultAggregationInput contains the context needed to aggregate one eval case result.
type EvalCaseResultAggregationInput struct {
	AppName         string                         // AppName identifies the app being evaluated.
	EvalSetID       string                         // EvalSetID identifies the eval set.
	EvalCase        *evalset.EvalCase              // EvalCase is the source eval case configuration.
	InferenceResult *InferenceResult               // InferenceResult is the inference output being evaluated.
	EvalMetrics     []*metric.EvalMetric           // EvalMetrics contains metrics that produced MetricResults in the same order.
	MetricResults   []*evalresult.EvalMetricResult // MetricResults contains the overall metric results for the eval case.
}

// EvalCaseResultAggregationResult contains the aggregated eval case result.
type EvalCaseResultAggregationResult struct {
	Score  float64           // Score is the finite case-level score.
	Status status.EvalStatus // Status is the case-level evaluation status.
}

type defaultEvalCaseResultAggregator struct{}

func (defaultEvalCaseResultAggregator) Aggregate(_ context.Context, input *EvalCaseResultAggregationInput) (*EvalCaseResultAggregationResult, error) {
	if input == nil {
		return nil, errors.New("eval case result aggregation input is nil")
	}
	finalStatus, err := istatus.SummarizeMetricsStatus(input.MetricResults)
	if err != nil {
		return nil, fmt.Errorf("summarize metric results: %w", err)
	}
	switch finalStatus {
	case status.EvalStatusPassed:
		return &EvalCaseResultAggregationResult{Score: 1, Status: finalStatus}, nil
	case status.EvalStatusFailed, status.EvalStatusNotEvaluated:
		return &EvalCaseResultAggregationResult{Score: 0, Status: finalStatus}, nil
	default:
		return nil, fmt.Errorf("unexpected eval status %v", finalStatus)
	}
}
