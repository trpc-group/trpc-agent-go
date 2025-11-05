//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/tooltrajectory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

type stubEvaluator struct {
	name        string
	description string
}

func (s *stubEvaluator) Name() string {
	return s.name
}

func (s *stubEvaluator) Description() string {
	return s.description
}

func (s *stubEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	return &evaluator.EvaluateResult{
		OverallScore:  42,
		OverallStatus: status.EvalStatusPassed,
	}, nil
}

func TestRegistryDefaults(t *testing.T) {
	reg := New()

	defaultName := tooltrajectory.New().Name()
	defaultEval, err := reg.Get(defaultName)
	assert.NoError(t, err)
	assert.NotNil(t, defaultEval)
	assert.Equal(t, defaultName, defaultEval.Name())
}

func TestRegistryRegisterAndGet(t *testing.T) {
	reg := New()
	custom := &stubEvaluator{name: "custom", description: "custom evaluator"}

	err := reg.Register("custom", custom)
	assert.NoError(t, err)

	got, err := reg.Get("custom")
	assert.NoError(t, err)
	assert.Equal(t, custom, got)
}

func TestRegistryOverwrite(t *testing.T) {
	reg := New()
	first := &stubEvaluator{name: "duplicate"}
	err := reg.Register("duplicate", first)
	assert.NoError(t, err)

	second := &stubEvaluator{name: "duplicate"}
	err = reg.Register("duplicate", second)
	assert.NoError(t, err)

	got, err := reg.Get("duplicate")
	assert.NoError(t, err)
	assert.Equal(t, second, got)
}

func TestRegistryRegisterDeriveName(t *testing.T) {
	reg := New()
	custom := &stubEvaluator{name: "derived"}

	err := reg.Register("", custom)
	assert.NoError(t, err)

	got, err := reg.Get("derived")
	assert.NoError(t, err)
	assert.Equal(t, custom, got)
}

func TestRegistryRegisterErrors(t *testing.T) {
	reg := New()

	err := reg.Register("nil", nil)
	assert.Error(t, err)

	err = reg.Register("", &stubEvaluator{})
	assert.Error(t, err)
}

func TestRegistryGetMissing(t *testing.T) {
	reg := New()

	_, err := reg.Get("missing")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}
