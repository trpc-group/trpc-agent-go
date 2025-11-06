//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCalculateMaxInputTokens tests the token tailoring calculation function.
func TestCalculateMaxInputTokens(t *testing.T) {
	tests := []struct {
		name            string
		contextWindow   int
		expectedMinSize int
		expectedMaxSize int
		description     string
	}{
		{
			name:            "claude-3-5-sonnet",
			contextWindow:   200000,
			expectedMinSize: 130000,
			expectedMaxSize: 130000,
			description:     "Claude model with 200k context window",
		},
		{
			name:            "gpt-4o",
			contextWindow:   128000,
			expectedMinSize: 83200,
			expectedMaxSize: 83200,
			description:     "GPT-4o with 128k context window",
		},
		{
			name:            "gpt-4-turbo",
			contextWindow:   128000,
			expectedMinSize: 83200,
			expectedMaxSize: 83200,
			description:     "GPT-4 Turbo with 128k context window",
		},
		{
			name:            "deepseek-chat",
			contextWindow:   131072,
			expectedMinSize: 85196,
			expectedMaxSize: 85196,
			description:     "DeepSeek with 131k context window",
		},
		{
			name:            "small-model",
			contextWindow:   8192,
			expectedMinSize: 1024,
			expectedMaxSize: 4813,
			description:     "Small model with 8k context window (calculated max < ratio limit)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			maxInputTokens := CalculateMaxInputTokens(tt.contextWindow)

			// Verify the result is within expected bounds.
			assert.GreaterOrEqual(t, maxInputTokens, tt.expectedMinSize,
				"max input tokens should be >= expectedMinSize for %s", tt.description)
			assert.Equal(t, tt.expectedMaxSize, maxInputTokens,
				"max input tokens should equal expectedMaxSize for %s", tt.description)
		})
	}
}

// TestTokenTailoringStrategy documents the token allocation formula.
func TestTokenTailoringStrategy(t *testing.T) {
	// This test documents the expected behavior of the token tailoring strategy.
	// Given a model with context window of 200,000 tokens (claude-3-5-sonnet).

	contextWindow := 200000
	maxInputTokens := CalculateMaxInputTokens(contextWindow)

	// Expected calculation breakdown:
	// - safetyMargin = 200000 × 0.10 = 20000 tokens
	// - calculatedMax = 200000 - 2048 - 512 - 20000 = 177440 tokens
	// - ratioLimit = 200000 × 0.65 = 130000 tokens
	// - maxInputTokens = min(177440, 130000) = 130000 tokens

	assert.Equal(t, 130000, maxInputTokens)

	// The allocation ensures:
	// - 65% (130k tokens) for input messages
	// - 1% (2048 tokens) reserved for output
	// - 0.25% (512 tokens) protocol overhead
	// - 10% (20k tokens) safety margin
	// - Remaining ~23.75% buffer for stability
	inputPercent := float64(maxInputTokens) / float64(contextWindow) * 100
	assert.InDelta(t, 65, inputPercent, 1)
}

// TestContextWindowResolution tests context window resolution for models used
// in token tailoring.
func TestContextWindowResolution(t *testing.T) {
	// Test models commonly used with token tailoring.
	tests := []struct {
		modelName      string
		expectedWindow int
	}{
		{"claude-3-5-sonnet", 200000},
		{"gpt-4o", 128000},
		{"deepseek-chat", 131072},
		{"o1-preview", 128000},
	}

	for _, tt := range tests {
		t.Run(tt.modelName, func(t *testing.T) {
			window := ResolveContextWindow(tt.modelName)
			require.Equal(t, tt.expectedWindow, window)
		})
	}
}
