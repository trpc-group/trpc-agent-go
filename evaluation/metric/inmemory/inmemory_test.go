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
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

func TestManagerLifecycle(t *testing.T) {
	ctx := context.Background()
	mgr := New().(*manager)

	names, err := mgr.List(ctx, "app", "set")
	assert.NoError(t, err)
	assert.Empty(t, names)

	err = mgr.Add(ctx, "app", "set", &metric.EvalMetric{MetricName: "accuracy", Threshold: 0.8})
	assert.NoError(t, err)

	names, err = mgr.List(ctx, "app", "set")
	assert.NoError(t, err)
	assert.Equal(t, []string{"accuracy"}, names)

	got, err := mgr.Get(ctx, "app", "set", "accuracy")
	assert.NoError(t, err)
	got.Threshold = 0.1

	fresh, err := mgr.Get(ctx, "app", "set", "accuracy")
	assert.NoError(t, err)
	assert.Equal(t, 0.8, fresh.Threshold)

	err = mgr.Update(ctx, "app", "set", &metric.EvalMetric{MetricName: "accuracy", Threshold: 0.9})
	assert.NoError(t, err)

	updated, err := mgr.Get(ctx, "app", "set", "accuracy")
	assert.NoError(t, err)
	assert.Equal(t, 0.9, updated.Threshold)

	err = mgr.Delete(ctx, "app", "set", "accuracy")
	assert.NoError(t, err)

	_, err = mgr.Get(ctx, "app", "set", "accuracy")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestManagerValidation(t *testing.T) {
	ctx := context.Background()
	mgr := New().(*manager)

	_, err := mgr.List(ctx, "", "set")
	assert.Error(t, err)
	_, err = mgr.List(ctx, "app", "")
	assert.Error(t, err)

	err = mgr.Add(ctx, "", "set", &metric.EvalMetric{MetricName: "m"})
	assert.Error(t, err)
	err = mgr.Add(ctx, "app", "", &metric.EvalMetric{MetricName: "m"})
	assert.Error(t, err)
	err = mgr.Add(ctx, "app", "set", nil)
	assert.Error(t, err)
	err = mgr.Add(ctx, "app", "set", &metric.EvalMetric{})
	assert.Error(t, err)

	_, err = mgr.Get(ctx, "", "set", "metric")
	assert.Error(t, err)
	_, err = mgr.Get(ctx, "app", "", "metric")
	assert.Error(t, err)
	_, err = mgr.Get(ctx, "app", "set", "")
	assert.Error(t, err)

	err = mgr.Update(ctx, "", "set", &metric.EvalMetric{MetricName: "m"})
	assert.Error(t, err)
	err = mgr.Update(ctx, "app", "", &metric.EvalMetric{MetricName: "m"})
	assert.Error(t, err)
	err = mgr.Update(ctx, "app", "set", nil)
	assert.Error(t, err)
	err = mgr.Update(ctx, "app", "set", &metric.EvalMetric{Threshold: 0.1})
	assert.Error(t, err)

	err = mgr.Delete(ctx, "", "set", "metric")
	assert.Error(t, err)
	err = mgr.Delete(ctx, "app", "", "metric")
	assert.Error(t, err)
	err = mgr.Delete(ctx, "app", "set", "")
	assert.Error(t, err)
}

func TestManagerDuplicateAndMissing(t *testing.T) {
	ctx := context.Background()
	mgr := New().(*manager)

	err := mgr.Add(ctx, "app", "set", &metric.EvalMetric{MetricName: "accuracy", Threshold: 0.8})
	assert.NoError(t, err)

	err = mgr.Add(ctx, "app", "set", &metric.EvalMetric{MetricName: "accuracy", Threshold: 0.9})
	assert.Error(t, err)

	err = mgr.Update(ctx, "app", "set", &metric.EvalMetric{MetricName: "missing"})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))

	err = mgr.Delete(ctx, "app", "set", "missing")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}
