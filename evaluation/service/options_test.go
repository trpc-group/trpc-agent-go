//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package service

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
)

func TestNewOptionsDefaults(t *testing.T) {
	opts := NewOptions()

	assert.NotNil(t, opts.EvalSetManager)
	assert.NotNil(t, opts.EvalResultManager)
	assert.NotNil(t, opts.Registry)
	assert.NotNil(t, opts.SessionIDSupplier)
	assert.Nil(t, opts.Callbacks)
	assert.Equal(t, runtime.GOMAXPROCS(0), opts.EvalCaseParallelism)
	assert.False(t, opts.EvalCaseParallelInferenceEnabled)
	assert.False(t, opts.EvalCaseParallelEvaluationEnabled)

	sessionID := opts.SessionIDSupplier(context.Background())
	assert.NotEmpty(t, sessionID)
}

func TestWithEvalSetManager(t *testing.T) {
	custom := evalsetinmemory.New()
	opts := NewOptions(WithEvalSetManager(custom))

	assert.Equal(t, custom, opts.EvalSetManager)
}

func TestWithEvalResultManager(t *testing.T) {
	custom := evalresultinmemory.New()
	opts := NewOptions(WithEvalResultManager(custom))

	assert.Equal(t, custom, opts.EvalResultManager)
}

func TestWithRegistry(t *testing.T) {
	custom := registry.New()
	opts := NewOptions(WithRegistry(custom))

	assert.Equal(t, custom, opts.Registry)
}

func TestWithSessionIDSupplier(t *testing.T) {
	called := false
	supplier := func(ctx context.Context) string {
		called = true
		return "session-custom"
	}

	opts := NewOptions(WithSessionIDSupplier(supplier))
	assert.Equal(t, "session-custom", opts.SessionIDSupplier(context.Background()))
	assert.True(t, called)
}

func TestWithCallbacks(t *testing.T) {
	callbacks := &Callbacks{}

	opts := NewOptions(WithCallbacks(callbacks))

	assert.Same(t, callbacks, opts.Callbacks)
}

func TestWithEvalCaseParallelism(t *testing.T) {
	opts := NewOptions(WithEvalCaseParallelism(3))
	assert.Equal(t, 3, opts.EvalCaseParallelism)
}

func TestWithEvalCaseParallelInferenceEnabled(t *testing.T) {
	opts := NewOptions(WithEvalCaseParallelInferenceEnabled(true))
	assert.True(t, opts.EvalCaseParallelInferenceEnabled)
}

func TestWithEvalCaseParallelEvaluationEnabled(t *testing.T) {
	opts := NewOptions(WithEvalCaseParallelEvaluationEnabled(true))
	assert.True(t, opts.EvalCaseParallelEvaluationEnabled)
}
