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
	"context"
	"fmt"
	"unicode/utf8"
)

const approxRunesPerToken = 4 // heuristic: ~1 token per 4 UTF-8 runes

// TokenTailoringConfig holds custom token tailoring budget parameters.
// This configuration allows advanced users to fine-tune the token allocation strategy.
type TokenTailoringConfig struct {
	// ProtocolOverheadTokens is the number of tokens reserved for protocol
	// overhead (request/response formatting).
	ProtocolOverheadTokens int
	// ReserveOutputTokens is the number of tokens reserved for output
	// generation.
	ReserveOutputTokens int
	// InputTokensFloor is the minimum number of input tokens.
	InputTokensFloor int
	// OutputTokensFloor is the minimum number of output tokens.
	OutputTokensFloor int
	// SafetyMarginRatio is the safety margin ratio for token counting
	// inaccuracies.
	SafetyMarginRatio float64
	// MaxInputTokensRatio is the maximum input tokens ratio of the context
	// window.
	MaxInputTokensRatio float64
}

// TokenCounter counts tokens for messages and tools.
// The implementation is model-agnostic to keep the model package lightweight.
type TokenCounter interface {
	// CountTokens returns the estimated token count for a single message.
	CountTokens(ctx context.Context, message Message) (int, error)

	// CountTokensRange returns the estimated token count for a range of messages.
	// This is more efficient than calling CountTokens multiple times.
	CountTokensRange(ctx context.Context, messages []Message, start, end int) (int, error)
}

// TailoringStrategy tailors messages to fit within a token budget.
type TailoringStrategy interface {
	// TailorMessages reduces message list so total tokens are within maxTokens.
	TailorMessages(ctx context.Context, messages []Message, maxTokens int) ([]Message, error)
}

// SimpleTokenCounter provides a very rough token estimation based on rune length.
// Heuristic: approximately one token per four UTF-8 runes for text content fields.
type SimpleTokenCounter struct{}

// NewSimpleTokenCounter creates a SimpleTokenCounter with a max token budget.
func NewSimpleTokenCounter() *SimpleTokenCounter {
	return &SimpleTokenCounter{}
}

// CountTokens estimates tokens for a single message.
func (c *SimpleTokenCounter) CountTokens(_ context.Context, message Message) (int, error) {
	total := 0

	// Count main content.
	total += utf8.RuneCountInString(message.Content) / approxRunesPerToken

	// Count reasoning content if present.
	if message.ReasoningContent != "" {
		total += utf8.RuneCountInString(message.ReasoningContent) / approxRunesPerToken
	}

	// Count text parts in multimodal content.
	for _, part := range message.ContentParts {
		if part.Text != nil {
			total += utf8.RuneCountInString(*part.Text) / approxRunesPerToken
		}
	}

	// Total should be at least 1 if message is not empty.
	if len(message.Content) > 0 {
		return max(total, 1), nil
	}
	return total, nil
}

// CountTokensRange estimates tokens for a range of messages.
func (c *SimpleTokenCounter) CountTokensRange(ctx context.Context, messages []Message, start, end int) (int, error) {
	if start < 0 || end > len(messages) || start >= end {
		return 0, fmt.Errorf("invalid range: start=%d, end=%d, len=%d", start, end, len(messages))
	}

	total := 0
	for i := start; i < end; i++ {
		// Ignore error because SimpleTokenCounter's CountTokens does not return error.
		tokens, _ := c.CountTokens(ctx, messages[i])
		total += tokens
	}
	return total, nil
}

// MiddleOutStrategy removes messages from the middle until within token budget.
//
// Background (Lost-in-the-Middle):
// Large context LLMs often exhibit positional bias: information at the beginning
// and end of a sequence tends to receive disproportionately higher attention,
// while content in the middle is comparatively neglected ("lost in the middle").
// Recent analyses describe a U-shaped "attention basin" where boundary items
// receive higher attention than mid-sequence items. See, for example, the
// attention-basin analysis and mitigation via attention-guided reranking in
// "Attention Basin: Why Contextual Position Matters in Large Language Models"
// (Yi et al., 2025). This phenomenon implies that when we must drop content to
// fit a context budget, removing mid-sequence items preferentially can be a
// reasonable heuristic because these items are less likely to be attended to
// compared to boundary content.
//
// Rationale:
//   - Preferentially preserve the head (earlier instructions/system prompts) and
//     the tail (most recent interaction), both of which are typically more salient
//     to generation due to positional bias.
//   - Remove from the middle first to minimize loss of impactful context.
//
// Note:
// This is a heuristic strategy. Depending on application semantics, HeadOut or
// TailOut may be preferable. When accurate token accounting is needed, pair this
// with a tiktoken-based counter. For details on positional bias, see arXiv:
// 2508.05128 (Attention Basin).
// After trimming, if the first message is a tool result, it will be removed.
type MiddleOutStrategy struct {
	tokenCounter TokenCounter
}

// NewMiddleOutStrategy constructs a middle-out strategy with the given counter.
func NewMiddleOutStrategy(counter TokenCounter) *MiddleOutStrategy {
	return &MiddleOutStrategy{
		tokenCounter: counter,
	}
}

// TailorMessages implements middle-out trimming with prefix sum optimization.
// Preserves system message and last turn, removes messages from the middle.
func (s *MiddleOutStrategy) TailorMessages(ctx context.Context, messages []Message, maxTokens int) ([]Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	// Build prefix sum for efficient range queries.
	prefixSum := buildPrefixSum(ctx, s.tokenCounter, messages)

	// Check if all messages fit within the budget.
	totalTokens := prefixSum[len(messages)]
	if totalTokens <= maxTokens {
		// All messages fit, but still remove leading tool message if present.
		result := messages
		if len(result) > 0 && result[0].Role == RoleTool {
			result = result[1:]
		}
		return result, nil
	}

	// Calculate preserved segments (always preserve system message and last turn).
	preservedHead := calculatePreservedHeadCount(messages)
	preservedTail := calculatePreservedTailCount(messages)

	// Calculate preserved token costs.
	headTokens := 0
	if preservedHead > 0 {
		headTokens = prefixSum[preservedHead]
	}

	tailTokens := 0
	if preservedTail > 0 {
		tailTokens = prefixSum[len(messages)] - prefixSum[len(messages)-preservedTail]
	}

	// If preserved segments exceed budget, return only preserved segments.
	if headTokens+tailTokens > maxTokens {
		return buildPreservedOnlyResult(messages, preservedHead, preservedTail), nil
	}

	// For MiddleOut, we need to find the optimal balance between head and tail messages.
	// We'll try different combinations of head and tail message counts.
	bestHeadCount, bestTailCount := s.findOptimalMiddleOutBalance(prefixSum, preservedHead, preservedTail, maxTokens)

	// Build result: system + head_messages + tail_messages + last_turn.
	result := s.buildMiddleOutResult(messages, preservedHead, bestHeadCount, bestTailCount, preservedTail)

	// Remove the first function execution result message if present.
	if len(result) > 0 && result[0].Role == RoleTool {
		result = result[1:]
	}
	return result, nil
}

// buildMiddleOutResult builds the final result with system + head + tail + last_turn.
func (s *MiddleOutStrategy) buildMiddleOutResult(messages []Message, preservedHead, headCount, tailCount, preservedTail int) []Message {
	result := []Message{}

	// Add preserved head (system message).
	if preservedHead > 0 {
		result = append(result, messages[:preservedHead]...)
	}

	// Add head messages.
	if headCount > preservedHead {
		result = append(result, messages[preservedHead:headCount]...)
	}

	// Add tail messages (excluding last turn).
	// tailCount is the start index for tail messages.
	if tailCount < len(messages)-preservedTail {
		result = append(result, messages[tailCount:len(messages)-preservedTail]...)
	}

	// Add preserved tail (last turn).
	if preservedTail > 0 {
		result = append(result, messages[len(messages)-preservedTail:]...)
	}

	return result
}

// findOptimalMiddleOutBalance finds the optimal balance between head and tail messages.
func (s *MiddleOutStrategy) findOptimalMiddleOutBalance(prefixSum []int, preservedHead, preservedTail, maxTokens int) (int, int) {
	// Simple approach: try to balance head and tail messages equally.
	// First, find how many messages we can fit total.
	totalMessages := len(prefixSum) - 1 - preservedHead - preservedTail
	availableTokens := maxTokens - (prefixSum[preservedHead] + (prefixSum[len(prefixSum)-1] - prefixSum[len(prefixSum)-1-preservedTail]))

	// If no tokens available for additional messages, return only preserved segments.
	if availableTokens <= 0 || totalMessages <= 0 {
		return preservedHead, len(prefixSum) - 1 - preservedTail
	}

	// Try to fit as many messages as possible, starting from the middle.
	headCount := preservedHead
	tailCount := len(prefixSum) - 1 - preservedTail

	// Binary search for the maximum number of messages we can fit.
	left, right := 0, totalMessages
	for left+1 < right {
		mid := (left + right) / 2

		// Try to fit mid messages from head and tail.
		headMessages := mid / 2
		tailMessages := mid - headMessages

		headTokens := prefixSum[preservedHead+headMessages] - prefixSum[preservedHead]
		tailTokens := prefixSum[len(prefixSum)-1-preservedTail] - prefixSum[len(prefixSum)-1-preservedTail-tailMessages]

		if headTokens+tailTokens <= availableTokens {
			left = mid
			headCount = preservedHead + headMessages
			tailCount = len(prefixSum) - 1 - preservedTail - tailMessages
		} else {
			right = mid
		}
	}

	return headCount, tailCount
}

// HeadOutStrategy deletes messages from the head (oldest first) until within limit.
// Preserves system message and last turn to maintain conversation context.
type HeadOutStrategy struct {
	tokenCounter TokenCounter
}

// NewHeadOutStrategy constructs a head-out strategy with the given counter.
func NewHeadOutStrategy(counter TokenCounter) *HeadOutStrategy {
	return &HeadOutStrategy{
		tokenCounter: counter,
	}
}

// TailorMessages removes from the head while respecting preservation options.
// For HeadOut, we preserve system message and last turn, then keep as many
// messages from the tail as possible within the token limit.
func (s *HeadOutStrategy) TailorMessages(ctx context.Context, messages []Message, maxTokens int) ([]Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	// Build prefix sum for efficient range queries.
	prefixSum := buildPrefixSum(ctx, s.tokenCounter, messages)

	// Check if all messages fit within the budget.
	totalTokens := prefixSum[len(messages)]
	if totalTokens <= maxTokens {
		// All messages fit, but still remove leading tool message if present.
		result := messages
		if len(result) > 0 && result[0].Role == RoleTool {
			result = result[1:]
		}
		return result, nil
	}

	// Calculate preserved segments (preserve system message and last turn).
	preservedHead := calculatePreservedHeadCount(messages)
	preservedTail := calculatePreservedTailCount(messages)

	// Calculate preserved token costs.
	headTokens := 0
	if preservedHead > 0 {
		headTokens = prefixSum[preservedHead]
	}

	tailTokens := 0
	if preservedTail > 0 {
		tailTokens = prefixSum[len(messages)] - prefixSum[len(messages)-preservedTail]
	}

	// If preserved segments exceed budget, return only preserved segments.
	if headTokens+tailTokens > maxTokens {
		return buildPreservedOnlyResult(messages, preservedHead, preservedTail), nil
	}

	// For HeadOut, find maximum messages from tail that fit with preserved head.
	maxTailCount := s.binarySearchMaxTailCount(prefixSum, preservedHead, preservedTail, maxTokens)

	// Build result: preserved head + tail messages + preserved tail.
	result := s.buildHeadOutResult(messages, preservedHead, maxTailCount, preservedTail)

	// Remove the first function execution result message if present.
	if len(result) > 0 && result[0].Role == RoleTool {
		result = result[1:]
	}
	return result, nil
}

// binarySearchMaxTailCount finds the maximum number of tail messages that fit with preserved head.
func (s *HeadOutStrategy) binarySearchMaxTailCount(prefixSum []int, preservedHead, preservedTail, maxTokens int) int {
	left, right := preservedHead, len(prefixSum)-1-preservedTail

	for left+1 < right {
		mid := (left + right) / 2

		// Calculate tokens for preserved head + tail messages + preserved tail.
		headTokens := prefixSum[preservedHead]
		tailTokens := prefixSum[len(prefixSum)-1-preservedTail] - prefixSum[mid]
		preservedTailTokens := prefixSum[len(prefixSum)-1] - prefixSum[len(prefixSum)-1-preservedTail]

		if headTokens+tailTokens+preservedTailTokens <= maxTokens {
			right = mid
		} else {
			left = mid
		}
	}

	return right
}

// buildHeadOutResult builds the final result with system + tail messages + last turn.
func (s *HeadOutStrategy) buildHeadOutResult(messages []Message, preservedHead, maxTailCount, preservedTail int) []Message {
	result := []Message{}

	// Add preserved head (system message).
	if preservedHead > 0 {
		result = append(result, messages[:preservedHead]...)
	}

	// Add tail messages (excluding last turn).
	if maxTailCount < len(messages)-preservedTail {
		result = append(result, messages[maxTailCount:len(messages)-preservedTail]...)
	}

	// Add preserved tail (last turn).
	if preservedTail > 0 {
		result = append(result, messages[len(messages)-preservedTail:]...)
	}

	return result
}

// TailOutStrategy deletes messages from the tail (newest first) until within limit.
// Preserves system message and last turn to maintain conversation context.
type TailOutStrategy struct {
	tokenCounter TokenCounter
}

// NewTailOutStrategy constructs a tail-out strategy with the given counter.
func NewTailOutStrategy(counter TokenCounter) *TailOutStrategy {
	return &TailOutStrategy{
		tokenCounter: counter,
	}
}

// TailorMessages removes from the tail while respecting preservation options.
// For TailOut, we preserve system message and last turn, then keep as many
// messages from the head as possible within the token limit.
func (s *TailOutStrategy) TailorMessages(ctx context.Context, messages []Message, maxTokens int) ([]Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	// Build prefix sum for efficient range queries.
	prefixSum := buildPrefixSum(ctx, s.tokenCounter, messages)

	// Check if all messages fit within the budget.
	totalTokens := prefixSum[len(messages)]
	if totalTokens <= maxTokens {
		// All messages fit, but still remove leading tool message if present.
		result := messages
		if len(result) > 0 && result[0].Role == RoleTool {
			result = result[1:]
		}
		return result, nil
	}

	// Calculate preserved segments (preserve system message and last turn).
	preservedHead := calculatePreservedHeadCount(messages)
	preservedTail := calculatePreservedTailCount(messages)

	// Calculate preserved token costs.
	headTokens := 0
	if preservedHead > 0 {
		headTokens = prefixSum[preservedHead]
	}

	tailTokens := 0
	if preservedTail > 0 {
		tailTokens = prefixSum[len(messages)] - prefixSum[len(messages)-preservedTail]
	}

	// If preserved segments exceed budget, return only preserved segments.
	if headTokens+tailTokens > maxTokens {
		return buildPreservedOnlyResult(messages, preservedHead, preservedTail), nil
	}

	// For TailOut, find maximum messages from head that fit with preserved tail.
	maxHeadCount := s.binarySearchMaxHeadCount(prefixSum, preservedHead, preservedTail, maxTokens)

	// Build result: preserved head + head messages + preserved tail.
	result := s.buildTailOutResult(messages, preservedHead, maxHeadCount, preservedTail)

	// Remove the first function execution result message if present.
	if len(result) > 0 && result[0].Role == RoleTool {
		result = result[1:]
	}
	return result, nil
}

// binarySearchMaxHeadCount finds the maximum number of head messages that fit with preserved tail.
func (s *TailOutStrategy) binarySearchMaxHeadCount(prefixSum []int, preservedHead, preservedTail, maxTokens int) int {
	left, right := preservedHead, len(prefixSum)-1-preservedTail

	for left+1 < right {
		mid := (left + right) / 2

		// Calculate tokens for preserved head + head messages + preserved tail.
		preservedHeadTokens := prefixSum[preservedHead]
		headTokens := prefixSum[mid] - prefixSum[preservedHead]
		preservedTailTokens := prefixSum[len(prefixSum)-1] - prefixSum[len(prefixSum)-1-preservedTail]

		if preservedHeadTokens+headTokens+preservedTailTokens <= maxTokens {
			left = mid
		} else {
			right = mid
		}
	}

	return left
}

// buildTailOutResult builds the final result with system + head messages + last turn.
func (s *TailOutStrategy) buildTailOutResult(messages []Message, preservedHead, maxHeadCount, preservedTail int) []Message {
	result := []Message{}

	// Add preserved head (system message).
	if preservedHead > 0 {
		result = append(result, messages[:preservedHead]...)
	}

	// Add head messages.
	if maxHeadCount > preservedHead {
		result = append(result, messages[preservedHead:maxHeadCount]...)
	}

	// Add preserved tail (last turn).
	if preservedTail > 0 {
		result = append(result, messages[len(messages)-preservedTail:]...)
	}

	return result
}

// calculatePreservedHeadCount calculates the number of preserved head messages.
// It preserves all consecutive system messages from the beginning.
func calculatePreservedHeadCount(messages []Message) int {
	count := 0
	for _, msg := range messages {
		// Stop at first non-system message.
		if msg.Role != RoleSystem {
			break
		}
		count++
	}
	return count
}

// calculatePreservedTailCount calculates the number of preserved tail messages (last turn).
// It finds the last complete user-assistant conversation pair from the end of the message list.
// This function is shared by all tailoring strategies for consistent behavior.
func calculatePreservedTailCount(messages []Message) int {
	if len(messages) == 0 {
		return 0
	}

	// Find the last assistant message from the end.
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]

		// Skip if not assistant message.
		if msg.Role != RoleAssistant {
			if msg.Role == RoleSystem {
				// Hit system message, no valid assistant found.
				return 1
			}
			continue
		}

		// Found assistant message, look for preceding user message.
		for j := i - 1; j >= 0; j-- {
			if messages[j].Role == RoleUser {
				// Found user message, return count from user to end.
				return len(messages) - j
			}
			if messages[j].Role == RoleSystem {
				// Hit system message, no user before assistant.
				return len(messages) - i
			}
		}

		// Assistant is first or preceded only by tool messages.
		return len(messages) - i
	}

	// No assistant message found, preserve last message.
	return 1
}

// buildPrefixSum builds a prefix sum array for message token counts.
// prefixSum[i] represents the cumulative token count from messages[0] to messages[i-1].
// This function is shared by all tailoring strategies for consistent token calculation.
func buildPrefixSum(ctx context.Context, tokenCounter TokenCounter, messages []Message) []int {
	prefixSum := make([]int, len(messages)+1)
	for i, msg := range messages {
		tokens, err := tokenCounter.CountTokens(ctx, msg)
		if err != nil {
			// In case of error, use a fallback estimation.
			prefixSum[i+1] = prefixSum[i] + utf8.RuneCountInString(msg.Content)/approxRunesPerToken
		} else {
			prefixSum[i+1] = prefixSum[i] + tokens
		}
	}
	return prefixSum
}

// buildPreservedOnlyResult builds result with only preserved head and tail segments.
// It removes any leading RoleTool messages from the result.
// This function is shared by all tailoring strategies for consistent behavior.
func buildPreservedOnlyResult(messages []Message, preservedHead, preservedTail int) []Message {
	result := []Message{}
	if preservedHead > 0 {
		result = append(result, messages[:preservedHead]...)
	}
	// Ensure preservedHead + preservedTail doesn't exceed total message count.
	if preservedTail > len(messages)-preservedHead {
		preservedTail = len(messages) - preservedHead
	}

	if preservedTail > 0 {
		result = append(result, messages[len(messages)-preservedTail:]...)
	}

	// Remove the first function execution result message if present.
	if len(result) > 0 && result[0].Role == RoleTool {
		result = result[1:]
	}
	return result
}
