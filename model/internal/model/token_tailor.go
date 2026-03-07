//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package model provides model-related functionality for internal usage.
package model

// Default budget constants for token tailoring.
// These constants define the token allocation strategy for automatic token tailoring.
const (
	// DefaultProtocolOverheadTokens is the number of tokens reserved for protocol overhead
	// (request/response formatting).
	DefaultProtocolOverheadTokens = 512

	// DefaultReserveOutputTokens is the number of tokens reserved for output generation.
	// This is typically ~1-2% of the typical context window.
	DefaultReserveOutputTokens = 2048

	// DefaultInputTokensFloor is the minimum number of input tokens to ensure
	// reasonable processing. Below this limit, the model may not function properly.
	DefaultInputTokensFloor = 1024

	// DefaultSafetyMarginRatio is the safety margin ratio (10%) used to account for
	// token counting inaccuracies. This provides a buffer between the calculated
	// limit and the actual model limits.
	DefaultSafetyMarginRatio = 0.10

	// DefaultMaxInputTokensRatio is the maximum input tokens ratio (100%) of the
	// context window.
	DefaultMaxInputTokensRatio = 1.0
)

// CalculateMaxInputTokens calculates the maximum input tokens based on the
// context window using the token tailoring formula.
//
// Formula:
//
//	safetyMargin = contextWindow × safetyMarginRatio (10%)
//	calculatedMax = contextWindow - reserveOutputTokens - protocolOverheadTokens - safetyMargin
//	ratioLimit = contextWindow × maxInputTokensRatio (100%)
//	maxInputTokens = max(min(calculatedMax, ratioLimit), inputTokensFloor)
//
// Example for deepseek-chat (contextWindow = 131072):
//
//	safetyMargin = 131072 × 0.10 = 13107 tokens
//	calculatedMax = 131072 - 2048 - 512 - 13107 = 115405 tokens
//	ratioLimit = 131072 × 1.0 = 131072 tokens
//	maxInputTokens = max(min(115405, 131072), 1024) = 115405 tokens (~88% of context window)
//
// This allocation ensures:
//   - ~88% of context window for input messages (limited by calculatedMax)
//   - ~1.5% (2048 tokens) reserved for output generation
//   - 10% safety margin for token counting inaccuracies
//   - Protocol overhead (512 tokens) for request/response formatting
func CalculateMaxInputTokens(contextWindow int) int {
	safetyMargin := int(float64(contextWindow) * DefaultSafetyMarginRatio)
	calculatedMax := max(contextWindow-DefaultReserveOutputTokens-
		DefaultProtocolOverheadTokens-safetyMargin, 0)
	ratioLimit := int(float64(contextWindow) * DefaultMaxInputTokensRatio)
	return max(min(calculatedMax, ratioLimit), DefaultInputTokensFloor)
}

// CalculateMaxInputTokensWithParams calculates the maximum input tokens
// with custom budget parameters.
func CalculateMaxInputTokensWithParams(
	contextWindow int,
	protocolOverheadTokens int,
	reserveOutputTokens int,
	inputTokensFloor int,
	safetyMarginRatio float64,
	maxInputTokensRatio float64,
) int {
	safetyMargin := int(float64(contextWindow) * safetyMarginRatio)
	calculatedMax := max(contextWindow-reserveOutputTokens-
		protocolOverheadTokens-safetyMargin, 0)
	ratioLimit := int(float64(contextWindow) * maxInputTokensRatio)
	return max(min(calculatedMax, ratioLimit), inputTokensFloor)
}
