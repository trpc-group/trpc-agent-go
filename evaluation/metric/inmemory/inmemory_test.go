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

	metricInput := &metric.EvalMetric{MetricName: "accuracy", Threshold: 0.8}
	err = mgr.Save(ctx, "app", "set", []*metric.EvalMetric{metricInput})
	assert.NoError(t, err)

	// Mutate the source and ensure stored copy is unaffected.
	metricInput.Threshold = 0.2
	stored := mgr.metrics["app"]["set"][0]
	assert.Equal(t, 0.8, stored.Threshold)

	names, err = mgr.List(ctx, "app", "set")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"accuracy"}, names)

	names, err = mgr.List(ctx, "app", "missing")
	assert.NoError(t, err)
	assert.Empty(t, names)

	got, err := mgr.Get(ctx, "app", "set", "accuracy")
	assert.NoError(t, err)
	assert.NotSame(t, stored, got)
	got.Threshold = 0.5
	fresh, err := mgr.Get(ctx, "app", "set", "accuracy")
	assert.NoError(t, err)
	assert.Equal(t, 0.8, fresh.Threshold)
}

func TestManagerGetErrors(t *testing.T) {
	ctx := context.Background()
	mgr := New().(*manager)

	_, err := mgr.Get(ctx, "app", "set", "metric")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
	assert.ErrorContains(t, err, "app app not found")

	err = mgr.Save(ctx, "app", "set", []*metric.EvalMetric{})
	assert.NoError(t, err)

	_, err = mgr.Get(ctx, "app", "other", "metric")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
	assert.ErrorContains(t, err, "eval set app.other not found")

	_, err = mgr.Get(ctx, "app", "set", "metric")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
	assert.ErrorContains(t, err, "metric app.set.metric not found")
}

func TestManagerValidation(t *testing.T) {
	ctx := context.Background()
	mgr := New().(*manager)

	_, err := mgr.List(ctx, "", "set")
	assert.EqualError(t, err, "empty app name")
	_, err = mgr.List(ctx, "app", "")
	assert.EqualError(t, err, "empty eval set id")

	err = mgr.Save(ctx, "", "set", nil)
	assert.EqualError(t, err, "empty app name")
	err = mgr.Save(ctx, "app", "", nil)
	assert.EqualError(t, err, "empty eval set id")

	_, err = mgr.Get(ctx, "", "set", "metric")
	assert.EqualError(t, err, "empty app name")
	_, err = mgr.Get(ctx, "app", "", "metric")
	assert.EqualError(t, err, "empty eval set id")
	_, err = mgr.Get(ctx, "app", "set", "")
	assert.EqualError(t, err, "empty metric name")
}

func TestManagerSaveCloneError(t *testing.T) {
	ctx := context.Background()
	mgr := New().(*manager)

	err := mgr.Save(ctx, "app", "set", []*metric.EvalMetric{nil})
	assert.Error(t, err)
	assert.ErrorContains(t, err, "clone metric")
}
