//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package openai

import (
	"encoding/json"
	"testing"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/respjson"
	"github.com/stretchr/testify/assert"
)

func TestGetCachedTokensWithDeepSeekFallback(t *testing.T) {
	tests := []struct {
		name           string
		standardCached int64
		extraFields    map[string]respjson.Field
		expected       int
	}{
		{
			name:           "standard field is non-zero, use it",
			standardCached: 10,
			extraFields:    map[string]respjson.Field{"prompt_cache_hit_tokens": respjson.NewField(`25`)},
			expected:       10,
		},
		{
			name:           "standard field is zero, fallback to DeepSeek field",
			standardCached: 0,
			extraFields:    map[string]respjson.Field{"prompt_cache_hit_tokens": respjson.NewField(`25`)},
			expected:       25,
		},
		{
			name:           "both fields zero",
			standardCached: 0,
			extraFields:    map[string]respjson.Field{},
			expected:       0,
		},
		{
			name:           "DeepSeek field missing",
			standardCached: 0,
			extraFields:    map[string]respjson.Field{},
			expected:       0,
		},
		{
			name:           "DeepSeek field malformed (string)",
			standardCached: 0,
			extraFields:    map[string]respjson.Field{"prompt_cache_hit_tokens": respjson.NewField(`"invalid"`)},
			expected:       0,
		},
		{
			name:           "DeepSeek field is negative",
			standardCached: 0,
			extraFields:    map[string]respjson.Field{"prompt_cache_hit_tokens": respjson.NewField(`-5`)},
			expected:       0,
		},
		{
			name:           "nil extra fields",
			standardCached: 0,
			extraFields:    nil,
			expected:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getCachedTokensWithDeepSeekFallback(tt.standardCached, tt.extraFields)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCompletionUsageToModelUsage_DeepSeekFallback(t *testing.T) {
	tests := []struct {
		name           string
		jsonData       string
		expectedPrompt int
		expectedTotal  int
		expectedCached int
		expectedReason int
	}{
		{
			name: "DeepSeek response with valid prompt_cache_hit_tokens",
			jsonData: `{
				"prompt_tokens": 100,
				"completion_tokens": 50,
				"total_tokens": 150,
				"prompt_tokens_details": {
					"cached_tokens": 0
				},
				"prompt_cache_hit_tokens": 25
			}`,
			expectedPrompt: 100,
			expectedTotal:  150,
			expectedCached: 25,
			expectedReason: 0,
		},
		{
			name: "DeepSeek response with malformed prompt_cache_hit_tokens",
			jsonData: `{
				"prompt_tokens": 100,
				"completion_tokens": 50,
				"total_tokens": 150,
				"prompt_tokens_details": {
					"cached_tokens": 0
				},
				"prompt_cache_hit_tokens": "invalid"
			}`,
			expectedPrompt: 100,
			expectedTotal:  150,
			expectedCached: 0,
			expectedReason: 0,
		},
		{
			name: "standard cached tokens takes precedence over DeepSeek field",
			jsonData: `{
				"prompt_tokens": 100,
				"completion_tokens": 50,
				"total_tokens": 150,
				"prompt_tokens_details": {
					"cached_tokens": 10
				},
				"prompt_cache_hit_tokens": 25
			}`,
			expectedPrompt: 100,
			expectedTotal:  150,
			expectedCached: 10,
			expectedReason: 0,
		},
		{
			name: "no ExtraFields",
			jsonData: `{
				"prompt_tokens": 100,
				"completion_tokens": 50,
				"total_tokens": 150,
				"prompt_tokens_details": {
					"cached_tokens": 0
				}
			}`,
			expectedPrompt: 100,
			expectedTotal:  150,
			expectedCached: 0,
			expectedReason: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var usage openai.CompletionUsage
			err := json.Unmarshal([]byte(tt.jsonData), &usage)
			assert.NoError(t, err)

			result := completionUsageToModelUsage(usage)

			assert.Equal(t, tt.expectedPrompt, result.PromptTokens)
			assert.Equal(t, tt.expectedTotal, result.TotalTokens)
			assert.Equal(t, tt.expectedCached, result.PromptTokensDetails.CachedTokens)
			assert.Equal(t, tt.expectedReason, result.CompletionTokensDetails.ReasoningTokens)
		})
	}
}

// TestCompletionUsageToModelUsage tests conversion from openai.CompletionUsage to model.Usage.
func TestCompletionUsageToModelUsage(t *testing.T) {
	t.Run("converts all fields correctly", func(t *testing.T) {
		openaiUsage := openai.CompletionUsage{
			PromptTokens:     int64(200),
			CompletionTokens: int64(75),
			TotalTokens:      int64(275),
			PromptTokensDetails: openai.CompletionUsagePromptTokensDetails{
				CachedTokens: int64(30),
			},
			CompletionTokensDetails: openai.CompletionUsageCompletionTokensDetails{
				ReasoningTokens: int64(25),
			},
		}

		result := completionUsageToModelUsage(openaiUsage)

		assert.Equal(t, 200, result.PromptTokens, "expected prompt tokens to be 200")
		assert.Equal(t, 75, result.CompletionTokens, "expected completion tokens to be 75")
		assert.Equal(t, 275, result.TotalTokens, "expected total tokens to be 275")
		assert.Equal(t, 30, result.PromptTokensDetails.CachedTokens, "expected cached tokens to be 30")
		assert.Equal(t, 25, result.CompletionTokensDetails.ReasoningTokens, "expected reasoning tokens to be 25")
	})

	t.Run("converts zero values", func(t *testing.T) {
		openaiUsage := openai.CompletionUsage{
			PromptTokens:     int64(0),
			CompletionTokens: int64(0),
			TotalTokens:      int64(0),
			PromptTokensDetails: openai.CompletionUsagePromptTokensDetails{
				CachedTokens: int64(0),
			},
			CompletionTokensDetails: openai.CompletionUsageCompletionTokensDetails{
				ReasoningTokens: int64(0),
			},
		}

		result := completionUsageToModelUsage(openaiUsage)

		assert.Equal(t, 0, result.PromptTokens, "expected prompt tokens to be 0")
		assert.Equal(t, 0, result.CompletionTokens, "expected completion tokens to be 0")
		assert.Equal(t, 0, result.TotalTokens, "expected total tokens to be 0")
		assert.Equal(t, 0, result.PromptTokensDetails.CachedTokens, "expected cached tokens to be 0")
		assert.Equal(t, 0, result.CompletionTokensDetails.ReasoningTokens, "expected reasoning tokens to be 0")
	})
}
