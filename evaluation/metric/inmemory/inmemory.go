//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides an in-memory metric manager implementation.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/clone"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

// manager implements metric.Manager backed by in-memory.
// Each API returns deep-copied objects to avoid accidental mutation.
type manager struct {
	mu      sync.RWMutex
	metrics map[string]map[string][]*metric.EvalMetric // appName -> evalSetID -> []*metric.EvalMetric.
}

// New creates a in-memory metric manager.
func New() metric.Manager {
	return &manager{
		metrics: make(map[string]map[string][]*metric.EvalMetric),
	}
}

// List lists all metric names identified by the given app name and eval set ID.
func (m *manager) List(_ context.Context, appName, evalSetID string) ([]string, error) {
	if appName == "" {
		return nil, errors.New("empty app name")
	}
	if evalSetID == "" {
		return nil, errors.New("empty eval set id")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	evalSets, ok := m.metrics[appName]
	if !ok {
		return []string{}, nil
	}
	metrics, ok := evalSets[evalSetID]
	if !ok {
		return []string{}, nil
	}
	metricNames := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		metricNames = append(metricNames, metric.MetricName)
	}
	return metricNames, nil
}

// Save stores the given metrics identified by the given app name and eval set ID.
func (m *manager) Save(_ context.Context, appName, evalSetID string, metrics []*metric.EvalMetric) error {
	if appName == "" {
		return errors.New("empty app name")
	}
	if evalSetID == "" {
		return errors.New("empty eval set id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureEvalSetExist(appName, evalSetID)
	clonedMetrics := make([]*metric.EvalMetric, 0, len(metrics))
	for _, metric := range metrics {
		cloned, err := clone.Clone(metric)
		if err != nil {
			return fmt.Errorf("clone metric: %w", err)
		}
		clonedMetrics = append(clonedMetrics, cloned)
	}
	m.metrics[appName][evalSetID] = clonedMetrics
	return nil
}

// Get gets a metric identified by the given app name, eval set ID and metric name.
func (m *manager) Get(ctx context.Context, appName, evalSetID, metricName string) (*metric.EvalMetric, error) {
	if appName == "" {
		return nil, errors.New("empty app name")
	}
	if evalSetID == "" {
		return nil, errors.New("empty eval set id")
	}
	if metricName == "" {
		return nil, errors.New("empty metric name")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	evalSets, ok := m.metrics[appName]
	if !ok {
		return nil, fmt.Errorf("app %s not found: %w", appName, os.ErrNotExist)
	}
	metrics, ok := evalSets[evalSetID]
	if !ok {
		return nil, fmt.Errorf("eval set %s.%s not found: %w", appName, evalSetID, os.ErrNotExist)
	}
	for _, metric := range metrics {
		if metric != nil && metric.MetricName == metricName {
			cloned, err := clone.Clone(metric)
			if err != nil {
				return nil, fmt.Errorf("clone metric: %w", err)
			}
			return cloned, nil
		}
	}
	return nil, fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricName, os.ErrNotExist)
}

func (m *manager) ensureEvalSetExist(appName, evalSetID string) {
	if _, ok := m.metrics[appName]; !ok {
		m.metrics[appName] = make(map[string][]*metric.EvalMetric)
	}
	if _, ok := m.metrics[appName][evalSetID]; !ok {
		m.metrics[appName][evalSetID] = []*metric.EvalMetric{}
	}
}
