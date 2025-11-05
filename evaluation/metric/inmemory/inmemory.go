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
	m.ensureEvalSetExist(appName, evalSetID)
	metricNames := make([]string, 0, len(m.metrics[appName][evalSetID]))
	for _, metric := range m.metrics[appName][evalSetID] {
		metricNames = append(metricNames, metric.MetricName)
	}
	return metricNames, nil
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
	m.ensureEvalSetExist(appName, evalSetID)
	for _, metric := range m.metrics[appName][evalSetID] {
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

// Add adds a metric to EvalSet identified by evalSetID.
func (m *manager) Add(ctx context.Context, appName, evalSetID string, metric *metric.EvalMetric) error {
	if appName == "" {
		return errors.New("empty app name")
	}
	if evalSetID == "" {
		return errors.New("empty eval set id")
	}
	if metric == nil {
		return errors.New("metric is nil")
	}
	if metric.MetricName == "" {
		return errors.New("metric name is empty")
	}
	m.ensureEvalSetExist(appName, evalSetID)
	for _, evalMetric := range m.metrics[appName][evalSetID] {
		if evalMetric != nil && evalMetric.MetricName == metric.MetricName {
			return fmt.Errorf("metric %s.%s.%s already exists", appName, evalSetID, metric.MetricName)
		}
	}
	m.metrics[appName][evalSetID] = append(m.metrics[appName][evalSetID], metric)
	return nil
}

// Delete deletes the metric from EvalSet identified by evalSetID and metricName.
func (m *manager) Delete(ctx context.Context, appName, evalSetID, metricName string) error {
	if appName == "" {
		return errors.New("empty app name")
	}
	if evalSetID == "" {
		return errors.New("empty eval set id")
	}
	if metricName == "" {
		return errors.New("metric name is empty")
	}
	m.ensureEvalSetExist(appName, evalSetID)
	metrics := m.metrics[appName][evalSetID]
	for i, evalMetric := range metrics {
		if evalMetric != nil && evalMetric.MetricName == metricName {
			m.metrics[appName][evalSetID] = append(metrics[:i], metrics[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricName, os.ErrNotExist)
}

// Update updates the metric identified by evalSetID and metric.MetricName.
func (m *manager) Update(ctx context.Context, appName, evalSetID string, metric *metric.EvalMetric) error {
	if appName == "" {
		return errors.New("empty app name")
	}
	if evalSetID == "" {
		return errors.New("empty eval set id")
	}
	if metric == nil {
		return errors.New("metric is nil")
	}
	if metric.MetricName == "" {
		return errors.New("metric name is empty")
	}
	m.ensureEvalSetExist(appName, evalSetID)
	for i, evalMetric := range m.metrics[appName][evalSetID] {
		if evalMetric != nil && evalMetric.MetricName == metric.MetricName {
			m.metrics[appName][evalSetID][i] = metric
			return nil
		}
	}
	return fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metric.MetricName, os.ErrNotExist)
}

func (m *manager) ensureEvalSetExist(appName, evalSetID string) {
	if _, ok := m.metrics[appName]; !ok {
		m.metrics[appName] = make(map[string][]*metric.EvalMetric)
	}
	if _, ok := m.metrics[appName][evalSetID]; !ok {
		m.metrics[appName][evalSetID] = []*metric.EvalMetric{}
	}
}
