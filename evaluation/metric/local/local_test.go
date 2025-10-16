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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
)

type fixedLocator struct {
	path string
}

func (f *fixedLocator) Build(_ string, _ string, _ string) string {
	return f.path
}

func TestLocalManagerLifecycle(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	mgr := New(metric.WithBaseDir(dir)).(*manager)

	names, err := mgr.List(ctx, "app", "set")
	assert.NoError(t, err)
	assert.Empty(t, names)

	err = mgr.Save(ctx, "app", "set", nil)
	assert.NoError(t, err)

	path := mgr.metricFilePath("app", "set")
	data, err := os.ReadFile(path)
	assert.NoError(t, err)
	var stored []*metric.EvalMetric
	err = json.Unmarshal(data, &stored)
	assert.NoError(t, err)
	assert.Empty(t, stored)

	input := []*metric.EvalMetric{{MetricName: "accuracy", Threshold: 0.9}}
	err = mgr.Save(ctx, "app", "set", input)
	assert.NoError(t, err)
	assert.FileExists(t, path)

	names, err = mgr.List(ctx, "app", "set")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"accuracy"}, names)

	got, err := mgr.Get(ctx, "app", "set", "accuracy")
	assert.NoError(t, err)
	assert.Equal(t, 0.9, got.Threshold)

	got.Threshold = 0.1
	fresh, err := mgr.Get(ctx, "app", "set", "accuracy")
	assert.NoError(t, err)
	assert.Equal(t, 0.9, fresh.Threshold)

	_, err = mgr.Get(ctx, "app", "set", "missing")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

func TestLocalManagerValidation(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	mgr := New(metric.WithBaseDir(dir)).(*manager)

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

func TestLocalManagerLoadError(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	mgr := New(metric.WithBaseDir(dir)).(*manager)

	path := mgr.metricFilePath("app", "set")
	err := os.MkdirAll(filepath.Dir(path), 0o755)
	assert.NoError(t, err)
	err = os.WriteFile(path, []byte("{invalid"), 0o644)
	assert.NoError(t, err)

	_, err = mgr.List(ctx, "app", "set")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "unmarshal metrics")

	_, err = mgr.Get(ctx, "app", "set", "metric")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "unmarshal metrics")
}

func TestLocalManagerStoreErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("mkdir failure", func(t *testing.T) {
		dir := t.TempDir()
		mgr := New(metric.WithBaseDir(dir)).(*manager)

		conflict := filepath.Join(dir, "app")
		err := os.WriteFile(conflict, []byte("x"), 0o644)
		assert.NoError(t, err)

		err = mgr.Save(ctx, "app", "set", []*metric.EvalMetric{{MetricName: "m", Threshold: 1}})
		assert.Error(t, err)
		assert.ErrorContains(t, err, "mkdir all")
	})

	t.Run("open file failure", func(t *testing.T) {
		dir := t.TempDir()
		loc := &fixedLocator{path: dir}
		mgr := New(metric.WithBaseDir(dir), metric.WithLocator(loc)).(*manager)

		err := mgr.Save(ctx, "app", "set", []*metric.EvalMetric{{MetricName: "m", Threshold: 1}})
		assert.Error(t, err)
		assert.ErrorContains(t, err, "rename file")
	})
}
