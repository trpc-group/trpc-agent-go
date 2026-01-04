//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package metric provides evaluation metrics.
package metric

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
)

// EvalMetric represents a metric used to evaluate a particular aspect of an eval case.
type EvalMetric struct {
	MetricName string               `json:"metricName,omitempty"` // MetricName identifies the metric.
	Threshold  float64              `json:"threshold,omitempty"`  // Threshold value for this metric.
	Criterion  *criterion.Criterion `json:"criterion,omitempty"`  // Evaluation criterion used by the metric.
}

// Manager defines the interface for managing evaluation metrics.
type Manager interface {
	// List returns all metric names identified by the given app name and eval set ID.
	List(ctx context.Context, appName, evalSetID string) ([]string, error)
	// Get gets a metric identified by the given app name, eval set ID and metric name.
	Get(ctx context.Context, appName, evalSetID, metricName string) (*EvalMetric, error)
	// Add adds a metric to EvalSet identified by evalSetID.
	Add(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error
	// Delete deletes the metric from EvalSet identified by evalSetID and metricName.
	Delete(ctx context.Context, appName, evalSetID, metricName string) error
	// Update updates the metric identified by evalSetID and metric.MetricName.
	Update(ctx context.Context, appName, evalSetID string, metric *EvalMetric) error
}
