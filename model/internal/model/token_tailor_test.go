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
)

func TestCalculateMaxOutputTokens(t *testing.T) {
	tests := []struct {
		name            string
		contextWindow   int
		usedInputTokens int
		expectedOutput  int
		expectZero      bool
	}{
		{
			name:            "Normal case - deepseek-chat with heavy input",
			contextWindow:   131072,
			usedInputTokens: 115383,
			// 131072 - 115383 - 512 - 13107 = 2070
			expectedOutput: 2070,
		},
		{
			name:            "Normal case - moderate input",
			contextWindow:   131072,
			usedInputTokens: 50000,
			// 131072 - 50000 - 512 - 13107 = 67453
			expectedOutput: 67453,
		},
		{
			name:            "Edge case - very low remaining tokens",
			contextWindow:   8192,
			usedInputTokens: 7500,
			// 8192 - 7500 - 512 - 819 = -639 (negative)
			// Should return max(negative, floor) = 0
			expectZero: true,
		},
		{
			name:            "Edge case - exactly at floor",
			contextWindow:   8192,
			usedInputTokens: 7000,
			// 8192 - 7000 - 512 - 819 = -139 (negative)
			// But if close, should return floor (256)
			expectZero: true,
		},
		{
			name:            "Small context window",
			contextWindow:   4096,
			usedInputTokens: 1000,
			// 4096 - 1000 - 512 - 409 = 2175
			expectedOutput: 2175,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateMaxOutputTokens(tt.contextWindow, tt.usedInputTokens)

			if tt.expectZero {
				if result != 0 {
					t.Errorf("Expected 0 tokens, got %d", result)
				}
				return
			}

			if result != tt.expectedOutput {
				safetyMargin := int(float64(tt.contextWindow) * DefaultSafetyMarginRatio)
				remaining := tt.contextWindow - tt.usedInputTokens - DefaultProtocolOverheadTokens - safetyMargin
				t.Errorf("Expected %d tokens, got %d\n"+
					"  contextWindow=%d, usedInputTokens=%d\n"+
					"  safetyMargin=%d, protocolOverhead=%d\n"+
					"  remaining=%d",
					tt.expectedOutput, result,
					tt.contextWindow, tt.usedInputTokens,
					safetyMargin, DefaultProtocolOverheadTokens,
					remaining)
			}

			// Verify total tokens don't exceed context window.
			safetyMargin := int(float64(tt.contextWindow) * DefaultSafetyMarginRatio)
			totalTokens := tt.usedInputTokens + result + DefaultProtocolOverheadTokens + safetyMargin
			if totalTokens > tt.contextWindow {
				t.Errorf("Total tokens (%d) exceed context window (%d)\n"+
					"  usedInputTokens=%d, maxOutputTokens=%d, protocolOverhead=%d, safetyMargin=%d",
					totalTokens, tt.contextWindow,
					tt.usedInputTokens, result, DefaultProtocolOverheadTokens, safetyMargin)
			}
		})
	}
}

func TestCalculateMaxInputTokens(t *testing.T) {
	tests := []struct {
		name           string
		contextWindow  int
		expectedOutput int
	}{
		{
			name:          "deepseek-chat (131072)",
			contextWindow: 131072,
			// safetyMargin = 13107
			// calculatedMax = 131072 - 2048 - 512 - 13107 = 115405
			// ratioLimit = 131072 × 1.0 = 131072
			// max(min(115405, 131072), 1024) = 115405
			expectedOutput: 115405,
		},
		{
			name:          "gpt-4 (8192)",
			contextWindow: 8192,
			// safetyMargin = 819
			// calculatedMax = 8192 - 2048 - 512 - 819 = 4813
			// ratioLimit = 8192 × 1.0 = 8192
			// max(min(4813, 8192), 1024) = 4813
			expectedOutput: 4813,
		},
		{
			name:          "Small context (2048)",
			contextWindow: 2048,
			// safetyMargin = 204
			// calculatedMax = 2048 - 2048 - 512 - 204 = -716 (negative, becomes 0)
			// ratioLimit = 2048 × 1.0 = 2048
			// max(min(0, 2048), 1024) = 1024 (floor)
			expectedOutput: 1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateMaxInputTokens(tt.contextWindow)
			if result != tt.expectedOutput {
				t.Errorf("Expected %d tokens, got %d for context window %d",
					tt.expectedOutput, result, tt.contextWindow)
			}
		})
	}
}
