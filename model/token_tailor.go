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

// defaultApproxRunesPerToken is the default approximate runes per token heuristic.
const defaultApproxRunesPerToken = 4.0

type simpleTokenCounterOptions struct {
	approxRunesPerToken float64
}

// SimpleTokenCounterOption configures a SimpleTokenCounter.
type SimpleTokenCounterOption func(*simpleTokenCounterOptions)

// WithApproxRunesPerToken sets the approximate runes per token heuristic.
// This is a heuristic and may vary across languages and models.
//
// Note:
// Values <= 0 are ignored and the default value is kept.
func WithApproxRunesPerToken(v float64) SimpleTokenCounterOption {
	return func(o *simpleTokenCounterOptions) {
		if v <= 0 {
			return
		}
		o.approxRunesPerToken = v
	}
}

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
// Heuristic: approximately one token per several UTF-8 runes for text fields.
type SimpleTokenCounter struct {
	approxRunesPerToken float64
}

// NewSimpleTokenCounter creates a SimpleTokenCounter.
func NewSimpleTokenCounter(opts ...SimpleTokenCounterOption) *SimpleTokenCounter {
	o := simpleTokenCounterOptions{
		approxRunesPerToken: defaultApproxRunesPerToken,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&o)
	}
	return &SimpleTokenCounter{approxRunesPerToken: o.approxRunesPerToken}
}

// CountTokens estimates tokens for a single message.
func (c *SimpleTokenCounter) CountTokens(_ context.Context, message Message) (int, error) {
	total := 0

	// Count main content.
	total += utf8.RuneCountInString(message.Content)

	// Count reasoning content if present.
	if message.ReasoningContent != "" {
		total += utf8.RuneCountInString(message.ReasoningContent)
	}

	// Count text parts in multimodal content.
	for _, part := range message.ContentParts {
		if part.Text != nil {
			total += utf8.RuneCountInString(*part.Text)
		}
	}

	// Count tool calls.
	for _, toolCall := range message.ToolCalls {
		total += c.countToolCallRunes(toolCall)
	}

	runesPerToken := c.approxRunesPerToken
	if runesPerToken <= 0 {
		// Fall back to default to avoid division by zero.
		runesPerToken = defaultApproxRunesPerToken
	}
	total = int(float64(total) / runesPerToken)

	// Total should be at least 1 if message is not empty.
	if isMessageNotEmpty(message) {
		return max(total, 1), nil
	}
	return total, nil
}

// isMessageNotEmpty checks if the message contains any content that should result in at least 1 token.
func isMessageNotEmpty(message Message) bool {
	// Check main content.
	if len(message.Content) > 0 {
		return true
	}

	// Check reasoning content.
	if len(message.ReasoningContent) > 0 {
		return true
	}

	// Check tool calls - any tool call with content should count.
	for _, toolCall := range message.ToolCalls {
		if toolCall.Type != "" || toolCall.ID != "" ||
			toolCall.Function.Name != "" || toolCall.Function.Description != "" ||
			len(toolCall.Function.Arguments) > 0 {
			return true
		}
	}
	return false
}

// countToolCallRunes calculates the rune count for a single tool call.
// This is used for simple token estimation based on character count.
func (c *SimpleTokenCounter) countToolCallRunes(toolCall ToolCall) int {
	total := 0

	// Count runes for tool call type (e.g., "function").
	total += utf8.RuneCountInString(toolCall.Type)

	// Count runes for tool call ID.
	total += utf8.RuneCountInString(toolCall.ID)

	// Count runes for function name.
	total += utf8.RuneCountInString(toolCall.Function.Name)

	// Count runes for function description.
	total += utf8.RuneCountInString(toolCall.Function.Description)

	// Count runes for function arguments (JSON string).
	total += utf8.RuneCount(toolCall.Function.Arguments)

	return total
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

type userAnchoredRound struct {
	start int
	end   int
}

// buildUserAnchoredRounds builds the user-anchored rounds for the messages.
func buildUserAnchoredRounds(messages []Message, preservedHead int) []userAnchoredRound {
	if preservedHead < 0 {
		preservedHead = 0
	}
	if preservedHead > len(messages) {
		preservedHead = len(messages)
	}

	hasAssistant := false
	for i := preservedHead; i < len(messages); i++ {
		if messages[i].Role == RoleAssistant {
			hasAssistant = true
			break
		}
	}

	var (
		rounds          []userAnchoredRound
		inRound         bool
		lastNonSystem   Role
		roundStart      int
		hasAnyNonSystem bool
	)

	flush := func(end int) {
		if !inRound {
			return
		}
		if end <= roundStart {
			return
		}
		rounds = append(rounds, userAnchoredRound{
			start: roundStart,
			end:   end,
		})
	}

	for i := preservedHead; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role == RoleSystem {
			continue
		}
		hasAnyNonSystem = true
		// If there is no assistant anywhere in the sequence, treat consecutive user
		// messages as separate rounds. This avoids collapsing large user-only
		// histories into a single untrimable round.
		if msg.Role == RoleUser && (!hasAssistant || lastNonSystem != RoleUser) {
			if inRound {
				flush(i)
			}
			inRound = true
			roundStart = i
		}
		lastNonSystem = msg.Role
	}

	if hasAnyNonSystem && inRound {
		flush(len(messages))
	}

	return rounds
}

// buildRoundTailoredResult builds the tailored result for the rounds.
func buildRoundTailoredResult(
	messages []Message,
	preservedHead int,
	rounds []userAnchoredRound,
	keep []bool,
) []Message {
	result := make([]Message, 0, len(messages))
	if preservedHead > 0 {
		result = append(result, messages[:preservedHead]...)
	}
	for i, r := range rounds {
		if i < 0 || i >= len(keep) || !keep[i] {
			continue
		}
		result = append(result, messages[r.start:r.end]...)
	}
	return result
}

// countTokensWithPrefixSum counts the tokens with the prefix sum.
func countTokensWithPrefixSum(prefixSum []int, start, end int) int {
	if start < 0 {
		start = 0
	}
	if end > len(prefixSum)-1 {
		end = len(prefixSum) - 1
	}
	if start >= end {
		return 0
	}
	return prefixSum[end] - prefixSum[start]
}

// countTokensForRounds counts the tokens for the rounds.
func countTokensForRounds(prefixSum []int, rounds []userAnchoredRound, keep []bool) int {
	total := 0
	for i, r := range rounds {
		if i < 0 || i >= len(keep) || !keep[i] {
			continue
		}
		total += countTokensWithPrefixSum(prefixSum, r.start, r.end)
	}
	return total
}

// ensureTailoredWithinBudget ensures the tailored result is within the budget.
func ensureTailoredWithinBudget(
	ctx context.Context,
	tokenCounter TokenCounter,
	messages []Message,
	maxTokens int,
) ([]Message, error) {
	done, result := shouldReturnOriginal(ctx, tokenCounter, messages, maxTokens)
	if done {
		return result, nil
	}

	preservedHead := calculatePreservedHeadCount(messages)
	withSystem, withoutSystem := buildMinimalSuffixCandidates(messages, preservedHead)
	if fitsWithinBudget(ctx, tokenCounter, withSystem, maxTokens) {
		return withSystem, nil
	}

	if len(withoutSystem) == 0 {
		return nil, nil
	}
	if fitsWithinBudget(ctx, tokenCounter, withoutSystem, maxTokens) {
		return withoutSystem, nil
	}
	return withoutSystem, nil
}

// shouldReturnOriginal checks if the messages should be returned as is.
func shouldReturnOriginal(
	ctx context.Context,
	tokenCounter TokenCounter,
	messages []Message,
	maxTokens int,
) (bool, []Message) {
	if len(messages) == 0 {
		return true, messages
	}
	if maxTokens <= 0 {
		return true, nil
	}
	tokens, err := tokenCounter.CountTokensRange(ctx, messages, 0, len(messages))
	if err != nil {
		return true, messages
	}
	if tokens <= maxTokens {
		return true, messages
	}
	return false, nil
}

// fitsWithinBudget checks if the messages fit within the budget.
func fitsWithinBudget(
	ctx context.Context,
	tokenCounter TokenCounter,
	messages []Message,
	maxTokens int,
) bool {
	if len(messages) == 0 {
		return false
	}
	tokens, err := tokenCounter.CountTokensRange(ctx, messages, 0, len(messages))
	if err != nil {
		return false
	}
	return tokens <= maxTokens
}

// buildMinimalSuffixCandidates builds the minimal suffix candidates for the messages.
func buildMinimalSuffixCandidates(messages []Message, preservedHead int) ([]Message, []Message) {
	last := lastNonSystemIndex(messages)
	if last < 0 {
		return nil, nil
	}

	last = trimTrailingAssistant(messages, last)
	if last < 0 {
		return nil, nil
	}

	start := startOfUserToolGroup(messages, last)
	if start < 0 {
		return nil, nil
	}

	end := last + 1

	withSystem := make([]Message, 0, preservedHead+(end-start))
	if preservedHead > 0 {
		withSystem = append(withSystem, messages[:preservedHead]...)
	}
	withSystem = append(withSystem, messages[start:end]...)
	withSystem = validateAndFixMessageSequence(withSystem)

	withoutSystem := validateAndFixMessageSequence(messages[start:end])
	return withSystem, withoutSystem
}

// lastNonSystemIndex finds the last non-system message index.
func lastNonSystemIndex(messages []Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != RoleSystem {
			return i
		}
	}
	return -1
}

// trimTrailingAssistant trims the trailing assistant messages.
func trimTrailingAssistant(messages []Message, last int) int {
	for last >= 0 && messages[last].Role == RoleAssistant {
		last--
	}
	return last
}

// startOfUserToolGroup finds the start of the user-tool group.
func startOfUserToolGroup(messages []Message, last int) int {
	start := last
	if last < 0 {
		return -1
	}
	if messages[last].Role != RoleTool {
		return start
	}
	for start >= 0 && messages[start].Role != RoleUser {
		start--
	}
	return start
}

// TailorMessages implements middle-out trimming with prefix sum optimization.
// Preserves system message and last turn, removes messages from the middle.
func (s *MiddleOutStrategy) TailorMessages(ctx context.Context, messages []Message, maxTokens int) ([]Message, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	prefixSum := buildPrefixSum(ctx, s.tokenCounter, messages)

	totalTokens := prefixSum[len(messages)]
	if totalTokens <= maxTokens {
		return validateAndFixMessageSequence(messages), nil
	}

	preservedHead := calculatePreservedHeadCount(messages)
	rounds := buildUserAnchoredRounds(messages, preservedHead)
	if len(rounds) == 0 {
		return validateAndFixMessageSequence(messages[:preservedHead]), nil
	}

	keep := make([]bool, len(rounds))
	for i := range keep {
		keep[i] = true
	}

	// Compute initial total once; subsequent updates are O(1) via prefix sums.
	headTokens := prefixSum[preservedHead]
	allRoundsTokens := countTokensForRounds(prefixSum, rounds, keep)
	currentTotal := headTokens + allRoundsTokens

	lastRoundIdx := len(rounds) - 1
	// Pre-allocate once, reuse each iteration to avoid repeated allocations.
	keptNonLast := make([]int, 0, lastRoundIdx)
	for {
		if currentTotal <= maxTokens {
			break
		}

		keptNonLast = keptNonLast[:0]
		for i := 0; i < lastRoundIdx; i++ {
			if keep[i] {
				keptNonLast = append(keptNonLast, i)
			}
		}
		if len(keptNonLast) == 0 {
			break
		}

		midPos := len(keptNonLast) / 2
		removeIdx := keptNonLast[midPos]
		// O(1) incremental update: subtract the removed round's tokens.
		removedTokens := countTokensWithPrefixSum(prefixSum, rounds[removeIdx].start, rounds[removeIdx].end)
		currentTotal -= removedTokens
		keep[removeIdx] = false
	}

	result := buildRoundTailoredResult(messages, preservedHead, rounds, keep)
	result = validateAndFixMessageSequence(result)
	return ensureTailoredWithinBudget(ctx, s.tokenCounter, result, maxTokens)
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

	prefixSum := buildPrefixSum(ctx, s.tokenCounter, messages)

	totalTokens := prefixSum[len(messages)]
	if totalTokens <= maxTokens {
		return validateAndFixMessageSequence(messages), nil
	}

	preservedHead := calculatePreservedHeadCount(messages)
	rounds := buildUserAnchoredRounds(messages, preservedHead)
	if len(rounds) == 0 {
		return validateAndFixMessageSequence(messages[:preservedHead]), nil
	}

	keep := make([]bool, len(rounds))
	for i := range keep {
		keep[i] = true
	}

	// Compute initial total once; subsequent updates are O(1) via prefix sums.
	headTokens := prefixSum[preservedHead]
	allRoundsTokens := countTokensForRounds(prefixSum, rounds, keep)
	currentTotal := headTokens + allRoundsTokens

	lastRoundIdx := len(rounds) - 1
	dropIdx := 0
	for {
		if currentTotal <= maxTokens {
			break
		}
		if dropIdx >= lastRoundIdx {
			break
		}
		// O(1) incremental update.
		removedTokens := countTokensWithPrefixSum(prefixSum, rounds[dropIdx].start, rounds[dropIdx].end)
		currentTotal -= removedTokens
		keep[dropIdx] = false
		dropIdx++
	}

	result := buildRoundTailoredResult(messages, preservedHead, rounds, keep)
	result = validateAndFixMessageSequence(result)
	return ensureTailoredWithinBudget(ctx, s.tokenCounter, result, maxTokens)
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

	prefixSum := buildPrefixSum(ctx, s.tokenCounter, messages)

	totalTokens := prefixSum[len(messages)]
	if totalTokens <= maxTokens {
		return validateAndFixMessageSequence(messages), nil
	}

	preservedHead := calculatePreservedHeadCount(messages)
	rounds := buildUserAnchoredRounds(messages, preservedHead)
	if len(rounds) == 0 {
		return validateAndFixMessageSequence(messages[:preservedHead]), nil
	}

	keep := make([]bool, len(rounds))
	for i := range keep {
		keep[i] = true
	}

	// Compute initial total once; subsequent updates are O(1) via prefix sums.
	headTokens := prefixSum[preservedHead]
	allRoundsTokens := countTokensForRounds(prefixSum, rounds, keep)
	currentTotal := headTokens + allRoundsTokens

	lastRoundIdx := len(rounds) - 1
	dropIdx := lastRoundIdx - 1
	for {
		if currentTotal <= maxTokens {
			break
		}
		if dropIdx < 0 {
			break
		}
		// O(1) incremental update.
		removedTokens := countTokensWithPrefixSum(prefixSum, rounds[dropIdx].start, rounds[dropIdx].end)
		currentTotal -= removedTokens
		keep[dropIdx] = false
		dropIdx--
	}

	result := buildRoundTailoredResult(messages, preservedHead, rounds, keep)
	result = validateAndFixMessageSequence(result)
	return ensureTailoredWithinBudget(ctx, s.tokenCounter, result, maxTokens)
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

// buildPrefixSum builds a prefix sum array for message token counts.
// prefixSum[i] represents the cumulative token count from messages[0] to messages[i-1].
// This function is shared by all tailoring strategies for consistent token calculation.
func buildPrefixSum(ctx context.Context, tokenCounter TokenCounter, messages []Message) []int {
	if tokenCounter == nil {
		tokenCounter = NewSimpleTokenCounter()
	}

	fallbackCounter := NewSimpleTokenCounter()
	prefixSum := make([]int, len(messages)+1)
	for i, msg := range messages {
		tokens, err := tokenCounter.CountTokens(ctx, msg)
		if err != nil {
			// Fall back to SimpleTokenCounter to keep estimation consistent.
			tokens, _ = fallbackCounter.CountTokens(ctx, msg)
		}
		prefixSum[i+1] = prefixSum[i] + tokens
	}
	return prefixSum
}
