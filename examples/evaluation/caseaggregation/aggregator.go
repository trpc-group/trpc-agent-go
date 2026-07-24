//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type weightedCaseAggregator struct {
	Threshold float64
}

type metricSettings struct {
	Weight float64
}

func (a weightedCaseAggregator) Aggregate(_ context.Context,
	input *service.EvalCaseResultAggregationInput) (*service.EvalCaseResultAggregationResult, error) {
	if input == nil {
		return nil, errors.New("aggregation input is nil")
	}
	weightedScore := 0.0
	totalWeight := 0.0
	for i, evalMetric := range input.EvalMetrics {
		if evalMetric == nil || i >= len(input.MetricResults) || input.MetricResults[i] == nil {
			continue
		}
		settings := readMetricSettings(evalMetric.Extension)
		weightedScore += input.MetricResults[i].Score * settings.Weight
		totalWeight += settings.Weight
	}
	if totalWeight == 0 {
		return &service.EvalCaseResultAggregationResult{
			Score:  0,
			Status: status.EvalStatusNotEvaluated,
		}, nil
	}
	score := weightedScore / totalWeight
	resultStatus := status.EvalStatusFailed
	if score >= a.Threshold {
		resultStatus = status.EvalStatusPassed
	}
	return &service.EvalCaseResultAggregationResult{
		Score:  score,
		Status: resultStatus,
	}, nil
}

func readMetricSettings(extension any) metricSettings {
	values, ok := extension.(map[string]any)
	if !ok {
		return metricSettings{Weight: 1}
	}
	settings := metricSettings{Weight: 1}
	if weight, ok := values["weight"].(float64); ok && weight > 0 {
		settings.Weight = weight
	}
	return settings
}
