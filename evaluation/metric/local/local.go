//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package local provides a local file storage implementation for metrics.
package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

const (
	defaultTempFileSuffix = ".tmp"
	defaultDirPermission  = 0o755
	defaultFilePermission = 0o644
)

type manager struct {
	mu      sync.RWMutex
	baseDir string
	locator metric.Locator
}

// New creates a filesystem-backed metric manager.
func New(opts ...metric.Option) metric.Manager {
	options := metric.NewOptions(opts...)
	return &manager{
		baseDir: options.BaseDir,
		locator: options.Locator,
	}
}

func (m *manager) List(_ context.Context, appName, evalSetID string) ([]string, error) {
	if appName == "" {
		return nil, errors.New("empty app name")
	}
	if evalSetID == "" {
		return nil, errors.New("empty eval set id")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	metrics, err := m.load(appName, evalSetID)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load metrics for app %s: %w", appName, err)
	}
	metricNames := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		metricNames = append(metricNames, metric.MetricName)
	}
	return metricNames, nil
}

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
	metrics, err := m.load(appName, evalSetID)
	if err != nil {
		return nil, fmt.Errorf("load metrics %s.%s of evalset: %w", appName, evalSetID, err)
	}
	for _, m := range metrics {
		if m != nil && m.MetricName == metricName {
			return m, nil
		}
	}
	return nil, fmt.Errorf("get metric %s: %w", metricName, os.ErrNotExist)
}

// Add adds a metric to EvalSet identified by evalSetID.
func (m *manager) Add(ctx context.Context, appName, evalSetID string, metricInput *metric.EvalMetric) error {
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
	metrics, err := m.load(appName, evalSetID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			metrics = []*metric.EvalMetric{}
		} else {
			return fmt.Errorf("load metrics of evalset %s.%s: %w", appName, evalSetID, err)
		}
	}
	for _, evalMetric := range metrics {
		if evalMetric != nil && evalMetric.MetricName == metricInput.MetricName {
			return fmt.Errorf("metric %s.%s.%s already exists", appName, evalSetID, metricInput.MetricName)
		}
	}
	metrics = append(metrics, metricInput)
	if err := m.store(appName, evalSetID, metrics); err != nil {
		return fmt.Errorf("store metrics of evalset %s.%s: %w", appName, evalSetID, err)
	}
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
	metrics, err := m.load(appName, evalSetID)
	if err != nil {
		return fmt.Errorf("load metrics of evalset %s.%s: %w", appName, evalSetID, err)
	}
	for i, evalMetric := range metrics {
		if evalMetric != nil && evalMetric.MetricName == metricName {
			metrics = append(metrics[:i], metrics[i+1:]...)
			if err := m.store(appName, evalSetID, metrics); err != nil {
				return fmt.Errorf("store metrics of evalset %s.%s: %w", appName, evalSetID, err)
			}
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
	metrics, err := m.load(appName, evalSetID)
	if err != nil {
		return fmt.Errorf("load metrics of evalset %s.%s: %w", appName, evalSetID, err)
	}
	for i, evalMetric := range metrics {
		if evalMetric != nil && evalMetric.MetricName == metric.MetricName {
			metrics[i] = metric
			if err := m.store(appName, evalSetID, metrics); err != nil {
				return fmt.Errorf("store metrics of evalset %s.%s: %w", appName, evalSetID, err)
			}
			return nil
		}
	}
	return fmt.Errorf("metric %s.%s.%s not found: %w", appName, evalSetID, metric.MetricName, os.ErrNotExist)
}

func (m *manager) metricFilePath(appName, evalSetID string) string {
	return m.locator.Build(m.baseDir, appName, evalSetID)
}

func (m *manager) load(appName, evalSetID string) ([]*metric.EvalMetric, error) {
	path := m.metricFilePath(appName, evalSetID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load metrics %s for app %s: %w", evalSetID, appName, err)
	}
	var metrics []*metric.EvalMetric
	if err := json.Unmarshal(data, &metrics); err != nil {
		return nil, fmt.Errorf("unmarshal metrics %s for app %s: %w", evalSetID, appName, err)
	}
	if metrics == nil {
		metrics = []*metric.EvalMetric{}
	}
	return metrics, nil
}

func (m *manager) store(appName, evalSetID string, metrics []*metric.EvalMetric) error {
	if metrics == nil {
		metrics = []*metric.EvalMetric{}
	}
	path := m.metricFilePath(appName, evalSetID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, defaultDirPermission); err != nil {
		return fmt.Errorf("mkdir all %s: %w", dir, err)
	}
	tmp := path + defaultTempFileSuffix
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, defaultFilePermission)
	if err != nil {
		return fmt.Errorf("open file %s: %w", tmp, err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(metrics); err != nil {
		file.Close()
		os.Remove(tmp)
		return fmt.Errorf("encode file %s: %w", tmp, err)
	}
	if err := file.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename file %s to %s: %w", tmp, path, err)
	}
	return nil
}
