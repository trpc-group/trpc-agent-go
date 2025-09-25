//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package metric

import (
	"context"
)

// Manager administers evaluation metrics used when running agent evaluations.
type Manager interface {
	// List returns all metrics configured for the given app + eval set.
	List(ctx context.Context, appName, evalSetID string) ([]*EvalMetric, error)
	// Save replaces the metrics configured for the given app + eval set.
	Save(ctx context.Context, appName, evalSetID string, metrics []*EvalMetric) error
	// Get fetches a single metric by name.
	Get(ctx context.Context, appName, evalSetID, metricName string) (*EvalMetric, error)
}
