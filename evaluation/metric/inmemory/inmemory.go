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

// Close implements metric.Manager.
func (m *manager) Close() error {
	return nil
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
	for _, evalMetric := range metrics {
		if evalMetric == nil {
			continue
		}
		metricNames = append(metricNames, evalMetric.MetricName)
	}
	return metricNames, nil
}

// Get gets a metric identified by the given app name, eval set ID and metric name.
func (m *manager) Get(_ context.Context, appName, evalSetID, metricName string) (*metric.EvalMetric, error) {
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
		return nil, fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricName, os.ErrNotExist)
	}
	metrics, ok := evalSets[evalSetID]
	if !ok {
		return nil, fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricName, os.ErrNotExist)
	}
	for _, evalMetric := range metrics {
		if evalMetric != nil && evalMetric.MetricName == metricName {
			cloned, err := clone.Clone(evalMetric)
			if err != nil {
				return nil, fmt.Errorf("clone metric: %w", err)
			}
			return cloned, nil
		}
	}
	return nil, fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricName, os.ErrNotExist)
}

// Add adds a metric to EvalSet identified by evalSetID.
func (m *manager) Add(_ context.Context, appName, evalSetID string, metricInput *metric.EvalMetric) error {
	if appName == "" {
		return errors.New("empty app name")
	}
	if evalSetID == "" {
		return errors.New("empty eval set id")
	}
	if metricInput == nil {
		return errors.New("metric is nil")
	}
	if metricInput.MetricName == "" {
		return errors.New("metric name is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	evalSets, ok := m.metrics[appName]
	if !ok {
		evalSets = make(map[string][]*metric.EvalMetric)
		m.metrics[appName] = evalSets
	}
	metrics := evalSets[evalSetID]
	for _, evalMetric := range metrics {
		if evalMetric != nil && evalMetric.MetricName == metricInput.MetricName {
			return fmt.Errorf("metric %s.%s.%s already exists", appName, evalSetID, metricInput.MetricName)
		}
	}
	cloned, err := clone.Clone(metricInput)
	if err != nil {
		return fmt.Errorf("clone metric: %w", err)
	}
	evalSets[evalSetID] = append(metrics, cloned)
	return nil
}

// Delete deletes the metric from EvalSet identified by evalSetID and metricName.
func (m *manager) Delete(_ context.Context, appName, evalSetID, metricName string) error {
	if appName == "" {
		return errors.New("empty app name")
	}
	if evalSetID == "" {
		return errors.New("empty eval set id")
	}
	if metricName == "" {
		return errors.New("metric name is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	evalSets, ok := m.metrics[appName]
	if !ok {
		return fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricName, os.ErrNotExist)
	}
	metrics, ok := evalSets[evalSetID]
	if !ok {
		return fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricName, os.ErrNotExist)
	}
	for i, evalMetric := range metrics {
		if evalMetric != nil && evalMetric.MetricName == metricName {
			evalSets[evalSetID] = append(metrics[:i], metrics[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricName, os.ErrNotExist)
}

// Update updates the metric identified by evalSetID and metric.MetricName.
func (m *manager) Update(_ context.Context, appName, evalSetID string, metricInput *metric.EvalMetric) error {
	if appName == "" {
		return errors.New("empty app name")
	}
	if evalSetID == "" {
		return errors.New("empty eval set id")
	}
	if metricInput == nil {
		return errors.New("metric is nil")
	}
	if metricInput.MetricName == "" {
		return errors.New("metric name is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	evalSets, ok := m.metrics[appName]
	if !ok {
		return fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricInput.MetricName, os.ErrNotExist)
	}
	metrics, ok := evalSets[evalSetID]
	if !ok {
		return fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricInput.MetricName, os.ErrNotExist)
	}
	for i, evalMetric := range metrics {
		if evalMetric != nil && evalMetric.MetricName == metricInput.MetricName {
			cloned, err := clone.Clone(metricInput)
			if err != nil {
				return fmt.Errorf("clone metric: %w", err)
			}
			m.metrics[appName][evalSetID][i] = cloned
			return nil
		}
	}
	return fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metricInput.MetricName, os.ErrNotExist)
}
