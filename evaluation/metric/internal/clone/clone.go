//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package clone provides functions to clone metrics.
package clone

import "trpc.group/trpc-go/trpc-agent-go/evaluation/metric"

// CloneMetric returns a defensive copy of the provided metric.
func CloneMetric(m *metric.EvalMetric) *metric.EvalMetric {
	if m == nil {
		return nil
	}
	clone := *m
	if m.JudgeModelOptions != nil {
		opts := *m.JudgeModelOptions
		clone.JudgeModelOptions = &opts
	}
	if m.Config != nil {
		config := make(map[string]interface{}, len(m.Config))
		for k, v := range m.Config {
			config[k] = v
		}
		clone.Config = config
	}
	return &clone
}

// CloneMetrics returns deep copies of the provided metrics slice.
func CloneMetrics(metrics []*metric.EvalMetric) []*metric.EvalMetric {
	if len(metrics) == 0 {
		return []*metric.EvalMetric{}
	}
	cloned := make([]*metric.EvalMetric, 0, len(metrics))
	for _, metric := range metrics {
		cloned = append(cloned, CloneMetric(metric))
	}
	return cloned
}
