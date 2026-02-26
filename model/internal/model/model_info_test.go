//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveContextWindow(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		expected  int
	}{
		{
			name:      "empty model name",
			modelName: "",
			expected:  defaultContextWindow,
		},
		{
			name:      "exact match - GPT-4",
			modelName: "gpt-4",
			expected:  8192,
		},
		{
			name:      "exact match - GPT-4o",
			modelName: "gpt-4o",
			expected:  128000,
		},
		{
			name:      "exact match - Claude-3-Opus",
			modelName: "claude-3-opus",
			expected:  200000,
		},
		{
			name:      "exact match - GPT-5.2",
			modelName: "gpt-5.2",
			expected:  400000,
		},
		{
			name:      "exact match - GPT-5.2-instant",
			modelName: "gpt-5.2-instant",
			expected:  400000,
		},
		{
			name:      "case insensitive match",
			modelName: "GPT-4O",
			expected:  128000,
		},
		{
			name:      "case insensitive match uppercase",
			modelName: "CLAUDE-3-OPUS",
			expected:  200000,
		},
		{
			name:      "prefix match - Gemini prefix",
			modelName: "gemini-1.5-pro",
			expected:  2097152,
		},
		{
			name:      "unknown model fallback",
			modelName: "unknown-model",
			expected:  defaultContextWindow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolveContextWindow(tt.modelName)
			assert.Equal(t, tt.expected, result, "ResolveContextWindow(%q) should return expected value", tt.modelName)
		})
	}
}

func TestGetAllModelContextWindows(t *testing.T) {
	// Test that GetAllModelContextWindows returns a copy
	original := GetAllModelContextWindows()

	require.NotNil(t, original, "GetAllModelContextWindows should not return nil")
	assert.NotEmpty(t, original, "GetAllModelContextWindows should not return empty map")

	// Test that modifying the returned map doesn't affect the original
	original["test-model"] = 12345
	after := GetAllModelContextWindows()

	assert.NotContains(t, after, "test-model", "Modifying returned map should not affect the original")
}

func TestConcurrentResolveContextWindow(t *testing.T) {
	// Test concurrent access to ResolveContextWindow
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()

			// Test various model names concurrently
			models := []string{"GPT-4", "GPT-4o", "Claude-3-Opus", "Gemini-1.5-Pro", "unknown-model"}
			for _, model := range models {
				result := ResolveContextWindow(model)
				assert.Positive(t, result, "ResolveContextWindow(%q) should return positive value", model)
			}
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}
