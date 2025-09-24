//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package evalresult provides evaluation result for evaluation set.
package evalresult

import "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"

// EvalMetricResult represents the result of a single metric evaluation.
type EvalMetricResult struct {
	// MetricName identifies the metric.
	MetricName string `json:"metric_name"`
	// Score obtained for this metric.
	Score float64 `json:"score,omitempty"`
	// Status of this metric evaluation.
	Status EvalStatus `json:"status"`
	// Threshold that was used.
	Threshold float64 `json:"threshold"`
	// Details contains additional metric-specific information.
	Details map[string]interface{} `json:"details,omitempty"`
}

// EvalMetricResultPerInvocation represents metric results for a single invocation.
type EvalMetricResultPerInvocation struct {
	ActualInvocation   *evalset.Invocation `json:"actual_invocation"`
	ExpectedInvocation *evalset.Invocation `json:"expected_invocation"`
	// MetricResults contains results for each metric for this invocation.
	MetricResults []*EvalMetricResult `json:"metric_results"`
}
