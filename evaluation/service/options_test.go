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
