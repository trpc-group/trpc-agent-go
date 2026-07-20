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

func TestCalculateMaxOutputTokens(t *testing.T) {
	// context 128000, used 100000 -> remaining 28000
	// safetyMargin = 12800, maxOut = 28000 - 512 - 12800 = 14688
	out := CalculateMaxOutputTokens(128000, 100000)
	if out != 14688 {
		t.Fatalf("expected 14688 output tokens, got %d", out)
	}

	zero := CalculateMaxOutputTokens(128000, 200000)
	if zero != 0 {
		t.Fatalf("expected 0 when input exceeds context, got %d", zero)
	}
}

func TestCalculateMaxOutputTokensWithParams_FloorCappedByRemaining(t *testing.T) {
	// remaining=100, floor=4096 -> capped floor 100
	out := CalculateMaxOutputTokensWithParams(1000, 900, 0, 4096, 0)
	if out != 100 {
		t.Fatalf("expected floor capped to remaining (100), got %d", out)
	}
}
