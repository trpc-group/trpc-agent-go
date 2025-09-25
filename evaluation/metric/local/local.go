//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/internal/clone"
)

type manager struct {
	mu       sync.RWMutex
	baseDir  string
	pathFunc metric.PathBuilder
}

// New creates a filesystem-backed metric manager.
func New(opts ...metric.Option) metric.Manager {
	options := metric.NewOptions(opts...)
	return &manager{
		baseDir:  options.BaseDir,
		pathFunc: options.PathBuilder,
	}
}

func (m *manager) metricPath(appName, evalSetID string) string {
	return m.pathFunc(m.baseDir, appName, evalSetID)
}

func (m *manager) List(_ context.Context, appName, evalSetID string) ([]*metric.EvalMetric, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	metrics, err := m.load(appName, evalSetID)
	if errors.Is(err, os.ErrNotExist) {
		return []*metric.EvalMetric{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load metrics %s for app %s: %w", evalSetID, appName, err)
	}
	return clone.CloneMetrics(metrics), nil
}

func (m *manager) Save(_ context.Context, appName, evalSetID string, metrics []*metric.EvalMetric) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store(appName, evalSetID, clone.CloneMetrics(metrics))
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

func (m *manager) load(appName, evalSetID string) ([]*metric.EvalMetric, error) {
	path := m.metricPath(appName, evalSetID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var metrics []*metric.EvalMetric
	if err := json.Unmarshal(data, &metrics); err != nil {
		return nil, err
	}
	if metrics == nil {
		metrics = []*metric.EvalMetric{}
	}
	return metrics, nil
}

func (m *manager) store(appName, evalSetID string, metrics []*metric.EvalMetric) error {
	path := m.metricPath(appName, evalSetID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir all %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open file %s: %w", tmp, err)
	}
	if metrics == nil {
		metrics = []*metric.EvalMetric{}
	}
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	if err := enc.Encode(metrics); err != nil {
		file.Close()
		return fmt.Errorf("encode metrics: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp, path, err)
	}
	return nil
}
