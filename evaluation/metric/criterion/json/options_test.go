//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package json

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewOptionsDefaults(t *testing.T) {
	opts := newOptions()
	assert.Nil(t, opts.numberTolerance)
}

func TestWithNumberTolerance(t *testing.T) {
	opts := newOptions(WithNumberTolerance(0.5))
	assert.NotNil(t, opts.numberTolerance)
	assert.InDelta(t, 0.5, *opts.numberTolerance, 1e-9)
}

func TestWithIgnoreAndMatchStrategy(t *testing.T) {
	opts := newOptions(WithIgnore(true), WithMatchStrategy(JSONMatchStrategyExact))
	assert.True(t, opts.ignore)
	assert.Equal(t, JSONMatchStrategyExact, opts.matchStrategy)
}

func TestWithIgnoreTreeAndCompare(t *testing.T) {
	called := false
	opts := newOptions(
		WithIgnoreTree(map[string]any{"k": true}),
		WithOnlyTree(map[string]any{"only": true}),
		WithCompare(func(actual, expected any) (bool, error) {
			called = true
			return true, nil
		}),
	)
	assert.Equal(t, map[string]any{"k": true}, opts.ignoreTree)
	assert.Equal(t, map[string]any{"only": true}, opts.onlyTree)
	assert.NotNil(t, opts.compare)
	_, _ = opts.compare(map[string]any{}, map[string]any{})
	assert.True(t, called)
}
