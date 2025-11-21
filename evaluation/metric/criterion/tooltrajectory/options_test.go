//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tooltrajectory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

func TestNewOptionsDefaults(t *testing.T) {
	opts := newOptions()
	assert.Equal(t, defaultToolTrajectoryStrategy, opts.defaultStrategy)
	assert.Nil(t, opts.toolStrategy)
	assert.False(t, opts.orderInsensitive)
	assert.Nil(t, opts.compare)
}

func TestWithDefault(t *testing.T) {
	custom := &ToolTrajectoryStrategy{}
	opts := newOptions(WithDefault(custom))
	assert.Equal(t, custom, opts.defaultStrategy)
}

func TestWithTool(t *testing.T) {
	tool := map[string]*ToolTrajectoryStrategy{
		"custom": {},
	}
	opts := newOptions(WithTool(tool))
	assert.Equal(t, tool, opts.toolStrategy)
}

func TestWithOrderInsensitive(t *testing.T) {
	opts := newOptions(WithOrderInsensitive(true))
	assert.True(t, opts.orderInsensitive)
}

func TestWithCompare(t *testing.T) {
	var called bool
	compare := func(actual, expected *evalset.Invocation) error {
		called = true
		return nil
	}
	opts := newOptions(WithCompare(compare))
	assert.NotNil(t, opts.compare)
	err := opts.compare(nil, nil)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestDefaultToolTrajectoryStrategyDeepEqualMismatch(t *testing.T) {
	errArgs := defaultToolTrajectoryStrategy.Arguments.Match(
		map[string]any{"a": 1},
		map[string]any{"a": 2},
	)
	assert.Error(t, errArgs)

	errResp := defaultToolTrajectoryStrategy.Response.Match(
		map[string]any{"r": 1},
		map[string]any{"r": 3},
	)
	assert.Error(t, errResp)
}
