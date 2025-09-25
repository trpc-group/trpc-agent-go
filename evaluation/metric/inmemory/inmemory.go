//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"fmt"
	"os"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/internal/clone"
)

type manager struct {
	mu      sync.RWMutex
	metrics map[string]map[string][]*metric.EvalMetric // app -> evalSet -> metrics
}

// New creates a new in-memory metric manager.
func New() metric.Manager {
	return &manager{
		metrics: make(map[string]map[string][]*metric.EvalMetric),
	}
}

func (m *manager) ensure(appName, evalSetID string) {
	if _, ok := m.metrics[appName]; !ok {
		m.metrics[appName] = make(map[string][]*metric.EvalMetric)
	}
	if _, ok := m.metrics[appName][evalSetID]; !ok {
		m.metrics[appName][evalSetID] = []*metric.EvalMetric{}
	}
}

func (m *manager) List(_ context.Context, appName, evalSetID string) ([]*metric.EvalMetric, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if sets, ok := m.metrics[appName]; ok {
		if metrics, ok := sets[evalSetID]; ok {
			return clone.CloneMetrics(metrics), nil
		}
	}
	return []*metric.EvalMetric{}, nil
}

func (m *manager) Save(_ context.Context, appName, evalSetID string, metrics []*metric.EvalMetric) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensure(appName, evalSetID)
	m.metrics[appName][evalSetID] = clone.CloneMetrics(metrics)
	return nil
}

func (m *manager) Get(ctx context.Context, appName, evalSetID, metricName string) (*metric.EvalMetric, error) {
	metrics, err := m.List(ctx, appName, evalSetID)
	if err != nil {
		return nil, err
	}
	for _, metric := range metrics {
		if metric != nil && metric.MetricName == metricName {
			return metric, nil
		}
	}
	return nil, fmt.Errorf("%w: metric %s", os.ErrNotExist, metricName)
}
