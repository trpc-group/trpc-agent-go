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
			name:      "exact match - GPT-5.4",
			modelName: "gpt-5.4",
			expected:  1050000,
		},
		{
			name:      "exact match - GPT-5.4-pro",
			modelName: "gpt-5.4-pro",
			expected:  1050000,
		},
		{
			name:      "exact match - GPT-5.2-instant",
			modelName: "gpt-5.2-instant",
			expected:  400000,
		},
		{
			name:      "exact match - Claude Sonnet 4.6",
			modelName: "claude-sonnet-4-6",
			expected:  1000000,
		},
		{
			name:      "exact match - Claude Opus 4.5 alias",
			modelName: "claude-opus-4-5",
			expected:  200000,
		},
		{
			name:      "exact match - Claude Sonnet 4.5 alias",
			modelName: "claude-sonnet-4-5",
			expected:  200000,
		},
		{
			name:      "exact match - Claude Haiku 4.5 alias",
			modelName: "claude-haiku-4-5",
			expected:  200000,
		},
		{
			name:      "exact match - GLM-5",
			modelName: "glm-5",
			expected:  200000,
		},
		{
			name:      "exact match - GLM-5 Hugging Face repo",
			modelName: "zai-org/glm-5",
			expected:  200000,
		},
		{
			name:      "exact match - GLM-4.5-Air Hugging Face repo",
			modelName: "zai-org/glm-4.5-air",
			expected:  128000,
		},
		{
			name:      "exact match - GLM-5.1",
			modelName: "glm-5.1",
			expected:  204800,
		},
		{
			name:      "exact match - Qwen3-Max",
			modelName: "qwen3-max",
			expected:  262144,
		},
		{
			name:      "exact match - Qwen3.5-Plus",
			modelName: "qwen3.5-plus",
			expected:  1000000,
		},
		{
			name:      "exact match - Qwen-Max",
			modelName: "qwen-max",
			expected:  131072,
		},
		{
			name:      "exact match - Qwen-Plus",
			modelName: "qwen-plus",
			expected:  1000000,
		},
		{
			name:      "exact match - DeepSeek chat",
			modelName: "deepseek-chat",
			expected:  131072,
		},
		{
			name:      "exact match - DeepSeek v4 pro",
			modelName: "deepseek-v4-pro",
			expected:  1000000,
		},
		{
			name:      "exact match - DeepSeek v4 flash",
			modelName: "deepseek-v4-flash",
			expected:  1000000,
		},
		{
			name:      "exact match - Kimi K2.5",
			modelName: "kimi-k2.5",
			expected:  256000,
		},
		{
			name:      "exact match - Kimi K2 legacy preview",
			modelName: "kimi-k2-0711-preview",
			expected:  128000,
		},
		{
			name:      "exact match - MiniMax M2.7",
			modelName: "minimax-m2.7",
			expected:  204800,
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
			name:      "longest prefix match - GPT-5.4 snapshot",
			modelName: "gpt-5.4-2026-03-05",
			expected:  1050000,
		},
		{
			name:      "longest prefix match - GPT-5.4 mini snapshot",
			modelName: "gpt-5.4-mini-2026-03-17",
			expected:  400000,
		},
		{
			name:      "prefix match - Claude alias snapshot with at separator",
			modelName: "claude-opus-4-5@20251101",
			expected:  200000,
		},
		{
			name:      "no prefix match without separator boundary",
			modelName: "gpt-5.4x",
			expected:  defaultContextWindow,
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

func TestLookupContextWindow(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		expected  int
		ok        bool
	}{
		{
			name:      "exact match",
			modelName: "gpt-4o",
			expected:  128000,
			ok:        true,
		},
		{
			name:      "case insensitive match",
			modelName: "GPT-4O",
			expected:  128000,
			ok:        true,
		},
		{
			name:      "prefix match",
			modelName: "gpt-4o-mini-preview",
			expected:  200000,
			ok:        true,
		},
		{
			name:      "new provider exact match",
			modelName: "minimax-m2.5-highspeed",
			expected:  204800,
			ok:        true,
		},
		{
			name:      "separator boundary prefix match",
			modelName: "glm-5@latest",
			expected:  200000,
			ok:        true,
		},
		{
			name:      "qwen snapshot prefix match",
			modelName: "qwen-max-2025-01-25",
			expected:  131072,
			ok:        true,
		},
		{
			name:      "hugging face repo exact match",
			modelName: "zai-org/glm-4.7-flash",
			expected:  200000,
			ok:        true,
		},
		{
			name:      "no boundary prefix match",
			modelName: "glm-5x",
			expected:  0,
			ok:        false,
		},
		{
			name:      "unknown model",
			modelName: "unknown-model",
			expected:  0,
			ok:        false,
		},
		{
			name:      "empty model",
			modelName: "",
			expected:  0,
			ok:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := LookupContextWindow(tt.modelName)
			assert.Equal(t, tt.expected, result)
			assert.Equal(t, tt.ok, ok)
		})
	}
}

func TestIsModelPrefixMatch(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		prefix    string
		expected  bool
	}{
		{
			name:      "exact match",
			modelName: "glm-5",
			prefix:    "glm-5",
			expected:  true,
		},
		{
			name:      "hyphen separator match",
			modelName: "qwen-max-2025-01-25",
			prefix:    "qwen-max",
			expected:  true,
		},
		{
			name:      "at separator match",
			modelName: "claude-opus-4-5@20251101",
			prefix:    "claude-opus-4-5",
			expected:  true,
		},
		{
			name:      "colon separator match",
			modelName: "glm-5:latest",
			prefix:    "glm-5",
			expected:  true,
		},
		{
			name:      "prefix without valid separator",
			modelName: "gpt-5.4x",
			prefix:    "gpt-5.4",
			expected:  false,
		},
		{
			name:      "no prefix match",
			modelName: "kimi-k2.5",
			prefix:    "glm-5",
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isModelPrefixMatch(tt.modelName, tt.prefix))
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
