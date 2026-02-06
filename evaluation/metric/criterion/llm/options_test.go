//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewOptionsDefaults(t *testing.T) {
	opts := newOptions()
	assert.Equal(t, DefaultNumSamples, opts.numSamples)
	require.NotNil(t, opts.generation)
	assert.Equal(t, defaultStream, opts.generation.Stream)
	require.NotNil(t, opts.generation.MaxTokens)
	assert.Equal(t, defaultMaxTokens, *opts.generation.MaxTokens)
	require.NotNil(t, opts.generation.Temperature)
	assert.Equal(t, defaultTemperature, *opts.generation.Temperature)
}

func TestOptionOverrides(t *testing.T) {
	gen := &model.GenerationConfig{Stream: true}
	opts := newOptions(
		WithVariant("deepseek"),
		WithBaseURL("base"),
		WithAPIKey("key"),
		WithExtraFields(map[string]any{"x": "y"}),
		WithNumSamples(3),
		WithGeneration(gen),
	)
	assert.Equal(t, "deepseek", opts.variant)
	assert.Equal(t, "base", opts.baseURL)
	assert.Equal(t, "key", opts.apiKey)
	require.Contains(t, opts.extraFields, "x")
	assert.Equal(t, "y", opts.extraFields["x"])
	assert.Equal(t, 3, opts.numSamples)
	assert.Equal(t, gen, opts.generation)
}
