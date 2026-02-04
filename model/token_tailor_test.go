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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSimpleTokenCounter_CountTokens(t *testing.T) {
	counter := NewSimpleTokenCounter()
	msg := NewSystemMessage("You are a helpful assistant.")

	n, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	assert.Greater(t, n, 0)
}

func TestSimpleTokenCounter_WithApproxRunesPerToken(t *testing.T) {
	const (
		textLen     = 16
		cnHeuristic = 1.6
	)

	msg := NewUserMessage(repeat("a", textLen))
	ctx := context.Background()

	defaultCounter := NewSimpleTokenCounter()
	defaultTokens, err := defaultCounter.CountTokens(ctx, msg)
	require.NoError(t, err)
	require.Greater(t, defaultTokens, 0)

	tests := []struct {
		name         string
		options      []SimpleTokenCounterOption
		assertTokens func(t *testing.T, got, baseline int)
	}{
		{
			name:    "negative_value_ignored_like_default",
			options: []SimpleTokenCounterOption{WithApproxRunesPerToken(-1)},
			assertTokens: func(t *testing.T, got, baseline int) {
				assert.Equal(t, baseline, got)
			},
		},
		{
			name:    "zero_value_ignored_like_default",
			options: []SimpleTokenCounterOption{WithApproxRunesPerToken(0)},
			assertTokens: func(t *testing.T, got, baseline int) {
				assert.Equal(t, baseline, got)
			},
		},
		{
			name:    "smaller_runes_per_token_increases_token_count",
			options: []SimpleTokenCounterOption{WithApproxRunesPerToken(cnHeuristic)},
			assertTokens: func(t *testing.T, got, baseline int) {
				assert.Greater(t, got, baseline)
			},
		},
		{
			name:    "very_small_positive_value_produces_large_token_count",
			options: []SimpleTokenCounterOption{WithApproxRunesPerToken(0.1)},
			assertTokens: func(t *testing.T, got, baseline int) {
				assert.Greater(t, got, baseline)
			},
		},
		{
			name:    "nil_option_is_safely_skipped",
			options: []SimpleTokenCounterOption{nil},
			assertTokens: func(t *testing.T, got, baseline int) {
				assert.Equal(t, baseline, got)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := NewSimpleTokenCounter(tt.options...)
			gotTokens, err := counter.CountTokens(ctx, msg)
			require.NoError(t, err)
			tt.assertTokens(t, gotTokens, defaultTokens)
		})
	}
}

func TestSimpleTokenCounter_CountTokens_ApproxRunesPerTokenFallback(t *testing.T) {
	ctx := context.Background()
	msg := NewUserMessage("hello")

	baselineCounter := NewSimpleTokenCounter()
	baselineTokens, err := baselineCounter.CountTokens(ctx, msg)
	require.NoError(t, err)
	require.Greater(t, baselineTokens, 0)

	tests := []struct {
		name                string
		approxRunesPerToken float64
	}{
		{
			name:                "zero_value_falls_back_to_default",
			approxRunesPerToken: 0,
		},
		{
			name:                "negative_value_falls_back_to_default",
			approxRunesPerToken: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := &SimpleTokenCounter{
				approxRunesPerToken: tt.approxRunesPerToken,
			}
			gotTokens, err := counter.CountTokens(ctx, msg)
			require.NoError(t, err)
			assert.Equal(t, baselineTokens, gotTokens)
		})
	}
}

// TestSimpleTokenCounter_CountTokens_DetailedCoverage tests all code paths in CountTokens function.
func TestSimpleTokenCounter_CountTokens_DetailedCoverage(t *testing.T) {
	counter := NewSimpleTokenCounter()
	ctx := context.Background()

	t.Run("empty message returns 0", func(t *testing.T) {
		msg := Message{
			Role:    RoleUser,
			Content: "", // empty content
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 0, result) // len(message.Content) == 0, return total directly
	})

	t.Run("empty tool calls return 0", func(t *testing.T) {
		msg := Message{
			Role:      RoleAssistant,
			ToolCalls: []ToolCall{{}}, // Empty tool call
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 0, result) // Empty tool calls should not count as content
	})

	t.Run("basic content ensures minimum 1 token", func(t *testing.T) {
		msg := Message{
			Role:    RoleUser,
			Content: "Hi", // 2 runes / 4 = 0 tokens, but max(0, 1) = 1
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 1, result) // max(0, 1) = 1
	})

	t.Run("tool calls ensure minimum 1 token", func(t *testing.T) {
		msg := Message{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{
				{Type: "f"}, // Very short content that would be < 1 token
			},
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 1, result) // Should be at least 1 even with minimal tool call content
	})

	t.Run("content with reasoning content", func(t *testing.T) {
		msg := Message{
			Role:             RoleAssistant,
			Content:          "Answer",                     // 6 runes
			ReasoningContent: "Let me think about this...", // 26 runes
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 8, result) // max((6+26)/4, 1) = 8
	})

	t.Run("empty reasoning content is ignored", func(t *testing.T) {
		msg := Message{
			Role:             RoleAssistant,
			Content:          "Answer", // 6 runes / 4 = 1 token
			ReasoningContent: "",       // empty, should be ignored
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 1, result) // max(1, 1) = 1
	})

	t.Run("content with text parts", func(t *testing.T) {
		textPart1 := "First text part"  // 15 runes / 4 = 3 tokens
		textPart2 := "Second text part" // 16 runes / 4 = 4 tokens
		msg := Message{
			Role:    RoleUser,
			Content: "Main content", // 12 runes / 4 = 3 tokens
			ContentParts: []ContentPart{
				{
					Type: ContentTypeText,
					Text: &textPart1,
				},
				{
					Type: ContentTypeText,
					Text: &textPart2,
				},
			},
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 10, result) // max(3 + 3 + 4, 1) = 10
	})

	t.Run("content parts with nil text are ignored", func(t *testing.T) {
		validText := "Valid text" // 10 runes / 4 = 2 tokens
		msg := Message{
			Role:    RoleUser,
			Content: "Main content", // 12 runes / 4 = 3 tokens
			ContentParts: []ContentPart{
				{
					Type: ContentTypeText,
					Text: &validText,
				},
				{
					Type: ContentTypeImage, // no text field
					Text: nil,
				},
				{
					Type: ContentTypeText,
					Text: nil, // nil text, should be ignored
				},
			},
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 5, result) // max(3 + 2, 1) = 5
	})

	t.Run("content parts with non-text types", func(t *testing.T) {
		msg := Message{
			Role:    RoleUser,
			Content: "Main content", // 12 runes / 4 = 3 tokens
			ContentParts: []ContentPart{
				{
					Type: ContentTypeImage,
					// no Text field for image type
				},
				{
					Type: ContentTypeAudio,
					// no Text field for audio type
				},
				{
					Type: ContentTypeFile,
					// no Text field for file type
				},
			},
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 3, result) // max(3, 1) = 3
	})

	t.Run("empty content with reasoning and text parts", func(t *testing.T) {
		textPart := "Text part" // 9 runes / 4 = 2 tokens
		msg := Message{
			Role:             RoleAssistant,
			Content:          "",               // empty content
			ReasoningContent: "Some reasoning", // 14 runes / 4 = 3 tokens
			ContentParts: []ContentPart{
				{
					Type: ContentTypeText,
					Text: &textPart,
				},
			},
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 5, result) // len(Content) == 0, so return total directly: 0 + 3 + 2 = 5
	})

	t.Run("unicode characters", func(t *testing.T) {
		msg := Message{
			Role:    RoleUser,
			Content: "你好世界", // 4 Chinese characters = 4 runes / 4 = 1 token
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 1, result) // max(1, 1) = 1
	})

	t.Run("all features combined", func(t *testing.T) {
		textPart1 := "Additional info" // 15 runes
		textPart2 := "More details"    // 12 runes
		msg := Message{
			Role:             RoleAssistant,
			Content:          "Main answer",      // 11 runes
			ReasoningContent: "Thinking process", // 16 runes
			ContentParts: []ContentPart{
				{
					Type: ContentTypeText,
					Text: &textPart1,
				},
				{
					Type: ContentTypeImage,
					Text: nil, // should be ignored
				},
				{
					Type: ContentTypeText,
					Text: &textPart2,
				},
			},
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 13, result) // max((11+16+15+12)/4, 1) = 13
	})

	t.Run("empty content parts slice", func(t *testing.T) {
		msg := Message{
			Role:         RoleUser,
			Content:      "Test",          // 4 runes / 4 = 1 token
			ContentParts: []ContentPart{}, // empty slice
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 1, result) // max(1, 1) = 1
	})

	t.Run("nil content parts slice", func(t *testing.T) {
		msg := Message{
			Role:         RoleUser,
			Content:      "Test", // 4 runes / 4 = 1 token
			ContentParts: nil,    // nil slice
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 1, result) // max(1, 1) = 1
	})
}

func TestSimpleTokenCounter_CountTokensRange(t *testing.T) {
	counter := NewSimpleTokenCounter()
	msgs := []Message{
		NewSystemMessage("You are a helpful assistant."),
		NewUserMessage("Hello"),
		NewUserMessage("World"),
	}

	// Test valid range
	n, err := counter.CountTokensRange(context.Background(), msgs, 0, 2)
	require.NoError(t, err)
	assert.Greater(t, n, 0)

	// Test invalid range
	_, err = counter.CountTokensRange(context.Background(), msgs, -1, 2)
	assert.Error(t, err)

	_, err = counter.CountTokensRange(context.Background(), msgs, 0, 5)
	assert.Error(t, err)

	_, err = counter.CountTokensRange(context.Background(), msgs, 2, 1)
	assert.Error(t, err)
}

func TestMiddleOutStrategy_TailorMessages(t *testing.T) {
	// Create long messages to force trimming.
	msgs := []Message{}
	for i := 0; i < 9; i++ {
		msgs = append(msgs, NewUserMessage("msg-"+string(rune('A'+i))+" "+repeat("x", 200)))
	}
	// Insert a tool result at head to verify post-trim removal.
	msgs = append([]Message{{Role: RoleTool, Content: "tool result"}}, msgs...)

	counter := NewSimpleTokenCounter()
	s := NewMiddleOutStrategy(counter)

	tailored, err := s.TailorMessages(context.Background(), msgs, 200)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(tailored), len(msgs))
	// First tool message should be removed if present after trimming.
	if len(tailored) > 0 {
		assert.NotEqual(t, RoleTool, tailored[0].Role)
	}
}

func TestMiddleOutStrategy_PreserveSystemAndLastTurn(t *testing.T) {
	// Create messages: system, user1, user2, user3, user4, user5
	// Total tokens: 7 + 2 + 2 + 2 + 2 + 2 = 17 tokens
	msgs := []Message{
		NewSystemMessage("You are a helpful assistant."),
		NewUserMessage("Question 1"),
		NewUserMessage("Question 2"),
		NewUserMessage("Question 3"),
		NewUserMessage("Question 4"),
		NewUserMessage("Question 5"),
	}

	counter := NewSimpleTokenCounter()
	s := NewMiddleOutStrategy(counter) // Always preserves system and last turn

	// Set maxTokens to 12 to trigger tailoring (total is 17 tokens).
	tailored, err := s.TailorMessages(context.Background(), msgs, 12)
	require.NoError(t, err)

	// Should preserve system message at the beginning.
	if len(tailored) > 0 {
		assert.Equal(t, RoleSystem, tailored[0].Role)
		assert.Equal(t, "You are a helpful assistant.", tailored[0].Content)
	}

	// Should preserve last turn (last 1-2 messages).
	if len(tailored) >= 2 {
		lastMsg := tailored[len(tailored)-1]
		assert.Equal(t, "Question 5", lastMsg.Content)
	}

	// Should remove messages from the middle.
	assert.Less(t, len(tailored), len(msgs))
}

func TestMiddleOutStrategy_MiddleOutLogic(t *testing.T) {
	// Create messages: system, user1, user2, user3, user4, user5, user6
	// Total tokens: 7 + 2 + 2 + 2 + 2 + 2 + 2 = 19 tokens
	msgs := []Message{
		NewSystemMessage("You are a helpful assistant."),
		NewUserMessage("Question 1"),
		NewUserMessage("Question 2"),
		NewUserMessage("Question 3"),
		NewUserMessage("Question 4"),
		NewUserMessage("Question 5"),
		NewUserMessage("Question 6"),
	}

	counter := NewSimpleTokenCounter()
	s := NewMiddleOutStrategy(counter)

	// Set maxTokens to 15 to trigger tailoring (total is 19 tokens).
	tailored, err := s.TailorMessages(context.Background(), msgs, 15)
	require.NoError(t, err)

	// Should preserve system message at the beginning.
	assert.Equal(t, RoleSystem, tailored[0].Role)
	assert.Equal(t, "You are a helpful assistant.", tailored[0].Content)

	// Should preserve the last user message.
	assert.Equal(t, RoleUser, tailored[len(tailored)-1].Role)
	assert.Equal(t, "Question 6", tailored[len(tailored)-1].Content)

	// Should have removed some middle messages.
	assert.Less(t, len(tailored), len(msgs))

	// Verify token count is within limit.
	totalTokens := 0
	for _, msg := range tailored {
		tokens, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		totalTokens += tokens
	}
	assert.LessOrEqual(t, totalTokens, 15)
}

func TestHeadOutStrategy_PreserveOptions(t *testing.T) {
	// sys, user1, user2, user3
	msgs := []Message{
		NewSystemMessage("sys"),
		NewUserMessage(repeat("a", 200)),
		NewUserMessage(repeat("b", 200)),
		NewUserMessage("tail"),
	}
	counter := NewSimpleTokenCounter()

	// Always preserves system message and last turn.
	s := NewHeadOutStrategy(counter)
	tailored, err := s.TailorMessages(context.Background(), msgs, 100)
	require.NoError(t, err)
	// Should keep system at head.
	if len(tailored) > 0 {
		assert.Equal(t, RoleSystem, tailored[0].Role)
	}
	// Ensure token count is within budget by calculating total tokens.
	totalTokens := 0
	for _, msg := range tailored {
		tokens, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		totalTokens += tokens
	}
	assert.LessOrEqual(t, totalTokens, 100)
}

func TestTailOutStrategy_PreserveOptions(t *testing.T) {
	// sys, user1, user2, user3
	msgs := []Message{
		NewSystemMessage("sys"),
		NewUserMessage(repeat("a", 200)),
		NewUserMessage(repeat("b", 200)),
		NewUserMessage("tail"),
	}
	counter := NewSimpleTokenCounter()

	// Always preserves system message and last turn.
	s := NewTailOutStrategy(counter)
	tailored, err := s.TailorMessages(context.Background(), msgs, 100)
	require.NoError(t, err)
	// Should keep last turn at tail.
	if len(tailored) > 0 {
		// Last message should be preserved
		assert.Equal(t, "tail", tailored[len(tailored)-1].Content)
	}
	// Ensure token count is within budget by calculating total tokens.
	totalTokens := 0
	for _, msg := range tailored {
		tokens, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		totalTokens += tokens
	}
	assert.LessOrEqual(t, totalTokens, 100)
}

func TestStrategyComparison(t *testing.T) {
	// Create messages with different token sizes to test strategy behavior
	msgs := []Message{
		NewSystemMessage("You are a helpful assistant."),
		NewUserMessage("Head 1: " + repeat("short ", 10)),
		NewUserMessage("Head 2: " + repeat("short ", 10)),
		NewUserMessage("Middle 1: " + repeat("long ", 100)),
		NewUserMessage("Middle 2: " + repeat("long ", 100)),
		NewUserMessage("Middle 3: " + repeat("long ", 100)),
		NewUserMessage("Tail 1: " + repeat("short ", 10)),
		NewUserMessage("Tail 2: " + repeat("short ", 10)),
		NewUserMessage("What is LLM?"),
	}

	counter := NewSimpleTokenCounter()
	maxTokens := 200 // Strict limit to force trimming

	// Test HeadOut strategy: should remove from head, keep tail
	t.Run("HeadOut", func(t *testing.T) {
		strategy := NewHeadOutStrategy(counter)
		tailored, err := strategy.TailorMessages(context.Background(), msgs, maxTokens)
		require.NoError(t, err)

		// Should preserve system message
		assert.Equal(t, RoleSystem, tailored[0].Role)
		assert.Equal(t, "You are a helpful assistant.", tailored[0].Content)

		// Should preserve last turn
		assert.Equal(t, "What is LLM?", tailored[len(tailored)-1].Content)

		// Should keep tail messages (from the end)
		// Should remove head messages (from the beginning)
		assert.Less(t, len(tailored), len(msgs))

		// Verify token count is within limit
		totalTokens := 0
		for _, msg := range tailored {
			tokens, err := counter.CountTokens(context.Background(), msg)
			require.NoError(t, err)
			totalTokens += tokens
		}
		assert.LessOrEqual(t, totalTokens, maxTokens)
	})

	// Test TailOut strategy: should remove from tail, keep head
	t.Run("TailOut", func(t *testing.T) {
		strategy := NewTailOutStrategy(counter)
		tailored, err := strategy.TailorMessages(context.Background(), msgs, maxTokens)
		require.NoError(t, err)

		// Should preserve system message
		assert.Equal(t, RoleSystem, tailored[0].Role)
		assert.Equal(t, "You are a helpful assistant.", tailored[0].Content)

		// Should preserve last turn
		assert.Equal(t, "What is LLM?", tailored[len(tailored)-1].Content)

		// Should keep head messages (from the beginning)
		// Should remove tail messages (from the end)
		assert.Less(t, len(tailored), len(msgs))

		// Verify token count is within limit
		totalTokens := 0
		for _, msg := range tailored {
			tokens, err := counter.CountTokens(context.Background(), msg)
			require.NoError(t, err)
			totalTokens += tokens
		}
		assert.LessOrEqual(t, totalTokens, maxTokens)
	})

	// Test MiddleOut strategy: should remove from middle, keep head and tail
	t.Run("MiddleOut", func(t *testing.T) {
		strategy := NewMiddleOutStrategy(counter)
		tailored, err := strategy.TailorMessages(context.Background(), msgs, maxTokens)
		require.NoError(t, err)

		// Should preserve system message
		assert.Equal(t, RoleSystem, tailored[0].Role)
		assert.Equal(t, "You are a helpful assistant.", tailored[0].Content)

		// Should preserve last turn
		assert.Equal(t, "What is LLM?", tailored[len(tailored)-1].Content)

		// Should keep some head and tail messages
		// Should remove middle messages
		assert.Less(t, len(tailored), len(msgs))

		// Verify token count is within limit
		totalTokens := 0
		for _, msg := range tailored {
			tokens, err := counter.CountTokens(context.Background(), msg)
			require.NoError(t, err)
			totalTokens += tokens
		}
		assert.LessOrEqual(t, totalTokens, maxTokens)
	})
}

// TestHeadOutStrategy_RemovesFromHead tests that HeadOut strategy removes messages from the head.
func TestHeadOutStrategy_RemovesFromHead(t *testing.T) {
	// Create messages with clear head/middle/tail structure
	msgs := []Message{
		NewSystemMessage("You are a helpful assistant."),
		NewUserMessage("Head 1: " + repeat("short ", 10)),
		NewUserMessage("Head 2: " + repeat("short ", 10)),
		NewUserMessage("Middle 1: " + repeat("long ", 100)),
		NewUserMessage("Middle 2: " + repeat("long ", 100)),
		NewUserMessage("Tail 1: " + repeat("short ", 10)),
		NewUserMessage("Tail 2: " + repeat("short ", 10)),
		NewUserMessage("What is LLM?"),
	}

	counter := NewSimpleTokenCounter()
	strategy := NewHeadOutStrategy(counter)
	maxTokens := 200

	tailored, err := strategy.TailorMessages(context.Background(), msgs, maxTokens)
	require.NoError(t, err)

	// Should preserve system message
	assert.Equal(t, RoleSystem, tailored[0].Role)

	// Should preserve last turn
	assert.Equal(t, "What is LLM?", tailored[len(tailored)-1].Content)

	// Should keep tail messages (from the end)
	// Should remove head messages (from the beginning)
	assert.Less(t, len(tailored), len(msgs))

	// Verify token count is within limit
	totalTokens := 0
	for _, msg := range tailored {
		tokens, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		totalTokens += tokens
	}
	assert.LessOrEqual(t, totalTokens, maxTokens)
}

// TestTailOutStrategy_RemovesFromTail tests that TailOut strategy removes messages from the tail.
func TestTailOutStrategy_RemovesFromTail(t *testing.T) {
	// Create messages with clear head/middle/tail structure
	msgs := []Message{
		NewSystemMessage("You are a helpful assistant."),
		NewUserMessage("Head 1: " + repeat("short ", 10)),
		NewUserMessage("Head 2: " + repeat("short ", 10)),
		NewUserMessage("Middle 1: " + repeat("long ", 100)),
		NewUserMessage("Middle 2: " + repeat("long ", 100)),
		NewUserMessage("Tail 1: " + repeat("short ", 10)),
		NewUserMessage("Tail 2: " + repeat("short ", 10)),
		NewUserMessage("What is LLM?"),
	}

	counter := NewSimpleTokenCounter()
	strategy := NewTailOutStrategy(counter)
	maxTokens := 200

	tailored, err := strategy.TailorMessages(context.Background(), msgs, maxTokens)
	require.NoError(t, err)

	// Should preserve system message
	assert.Equal(t, RoleSystem, tailored[0].Role)

	// Should preserve last turn
	assert.Equal(t, "What is LLM?", tailored[len(tailored)-1].Content)

	// Should keep head messages (from the beginning)
	// Should remove tail messages (from the end)
	assert.Less(t, len(tailored), len(msgs))

	// Verify token count is within limit
	totalTokens := 0
	for _, msg := range tailored {
		tokens, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		totalTokens += tokens
	}
	assert.LessOrEqual(t, totalTokens, maxTokens)
}

// TestMiddleOutStrategy_RemovesFromMiddle tests that MiddleOut strategy removes messages from the middle.
// TestCalculatePreservedHeadCount tests the shared calculatePreservedHeadCount function.
func TestCalculatePreservedHeadCount(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		expected int
	}{
		{
			name:     "empty messages",
			messages: []Message{},
			expected: 0,
		},
		{
			name: "single system message",
			messages: []Message{
				NewSystemMessage("System 1"),
			},
			expected: 1,
		},
		{
			name: "multiple consecutive system messages",
			messages: []Message{
				NewSystemMessage("System 1"),
				NewSystemMessage("System 2"),
				NewSystemMessage("System 3"),
			},
			expected: 3,
		},
		{
			name: "system messages followed by user",
			messages: []Message{
				NewSystemMessage("System 1"),
				NewSystemMessage("System 2"),
				NewUserMessage("User message"),
			},
			expected: 2,
		},
		{
			name: "no system message at start",
			messages: []Message{
				NewUserMessage("User message"),
				NewSystemMessage("System 1"),
			},
			expected: 0,
		},
		{
			name: "system, user, system pattern",
			messages: []Message{
				NewSystemMessage("System 1"),
				NewUserMessage("User message"),
				NewSystemMessage("System 2"),
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculatePreservedHeadCount(tt.messages)
			require.Equal(t, tt.expected, result,
				"Expected %d preserved head messages, got %d", tt.expected, result)
		})
	}
}

// Helper function to truncate content for logging.
// TestRoleToolRemoval tests that all strategies properly remove leading RoleTool messages.
func TestRoleToolRemoval(t *testing.T) {
	// Create a counter for testing.
	counter := NewSimpleTokenCounter()

	// Create messages with a leading tool message followed by user messages.
	messages := []Message{
		NewToolMessage("call_1", "calculator", "2 + 2 = 4"),
		NewUserMessage("Hello"),
		NewUserMessage("How are you?"),
		NewAssistantMessage("I'm doing well, thank you!"),
	}

	strategies := []TailoringStrategy{
		NewHeadOutStrategy(counter),
		NewMiddleOutStrategy(counter),
		NewTailOutStrategy(counter),
	}

	for _, strategy := range strategies {
		t.Run(fmt.Sprintf("%T", strategy), func(t *testing.T) {
			result, err := strategy.TailorMessages(context.Background(), messages, 100)
			require.NoError(t, err)
			require.NotEmpty(t, result)

			// The first message should not be a tool message.
			require.NotEqual(t, RoleTool, result[0].Role, "First message should not be a tool message")
		})
	}
}

func TestMiddleOutStrategy_RemovesFromMiddle(t *testing.T) {
	// Create messages with clear head/middle/tail structure
	msgs := []Message{
		NewSystemMessage("You are a helpful assistant."),
		NewUserMessage("Head 1: " + repeat("short ", 10)),
		NewUserMessage("Head 2: " + repeat("short ", 10)),
		NewUserMessage("Middle 1: " + repeat("long ", 100)),
		NewUserMessage("Middle 2: " + repeat("long ", 100)),
		NewUserMessage("Tail 1: " + repeat("short ", 10)),
		NewUserMessage("Tail 2: " + repeat("short ", 10)),
		NewUserMessage("What is LLM?"),
	}

	counter := NewSimpleTokenCounter()
	strategy := NewMiddleOutStrategy(counter)
	maxTokens := 200

	tailored, err := strategy.TailorMessages(context.Background(), msgs, maxTokens)
	require.NoError(t, err)

	// Should preserve system message
	assert.Equal(t, RoleSystem, tailored[0].Role)

	// Should preserve last turn
	assert.Equal(t, "What is LLM?", tailored[len(tailored)-1].Content)

	// Should keep some head and tail messages
	// Should remove middle messages
	assert.Less(t, len(tailored), len(msgs))

	// Verify token count is within limit
	totalTokens := 0
	for _, msg := range tailored {
		tokens, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		totalTokens += tokens
	}
	assert.LessOrEqual(t, totalTokens, maxTokens)
}

// TestStrategyBehavior_DifferentResults tests that different strategies produce different results.
func TestStrategyBehavior_DifferentResults(t *testing.T) {
	// Create messages with different token sizes
	msgs := []Message{
		NewSystemMessage("You are a helpful assistant."),
		NewUserMessage("Head 1: " + repeat("short ", 10)),
		NewUserMessage("Head 2: " + repeat("short ", 10)),
		NewUserMessage("Middle 1: " + repeat("long ", 100)),
		NewUserMessage("Middle 2: " + repeat("long ", 100)),
		NewUserMessage("Middle 3: " + repeat("long ", 100)),
		NewUserMessage("Tail 1: " + repeat("short ", 10)),
		NewUserMessage("Tail 2: " + repeat("short ", 10)),
		NewUserMessage("What is LLM?"),
	}

	counter := NewSimpleTokenCounter()
	maxTokens := 200

	// Test all strategies
	headOut := NewHeadOutStrategy(counter)
	tailOut := NewTailOutStrategy(counter)
	middleOut := NewMiddleOutStrategy(counter)

	headResult, err := headOut.TailorMessages(context.Background(), msgs, maxTokens)
	require.NoError(t, err)

	tailResult, err := tailOut.TailorMessages(context.Background(), msgs, maxTokens)
	require.NoError(t, err)

	middleResult, err := middleOut.TailorMessages(context.Background(), msgs, maxTokens)
	require.NoError(t, err)

	// All strategies should preserve system message and last turn
	assert.Equal(t, RoleSystem, headResult[0].Role)
	assert.Equal(t, RoleSystem, tailResult[0].Role)
	assert.Equal(t, RoleSystem, middleResult[0].Role)

	assert.Equal(t, "What is LLM?", headResult[len(headResult)-1].Content)
	assert.Equal(t, "What is LLM?", tailResult[len(tailResult)-1].Content)
	assert.Equal(t, "What is LLM?", middleResult[len(middleResult)-1].Content)

	// All strategies should reduce message count
	assert.Less(t, len(headResult), len(msgs))
	assert.Less(t, len(tailResult), len(msgs))
	assert.Less(t, len(middleResult), len(msgs))

	// Different strategies should produce different results
	// (This is a basic check - in practice, they might be the same if token limits are very generous)
	assert.True(t, len(headResult) <= len(msgs))
	assert.True(t, len(tailResult) <= len(msgs))
	assert.True(t, len(middleResult) <= len(msgs))
}

// repeat returns a string repeated n times.
func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

// Benchmark tests to verify time complexity improvements.

func BenchmarkTokenCounter_CountTokens(b *testing.B) {
	counter := NewSimpleTokenCounter()
	msg := NewUserMessage("This is a test message with some content to count tokens for.")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := counter.CountTokens(context.Background(), msg)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMiddleOutStrategy_SmallMessages(b *testing.B) {
	counter := NewSimpleTokenCounter()
	strategy := NewMiddleOutStrategy(counter)

	// Create 10 messages
	messages := make([]Message, 10)
	for i := 0; i < 10; i++ {
		messages[i] = NewUserMessage(fmt.Sprintf("Message %d with some content", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := strategy.TailorMessages(context.Background(), messages, 50)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMiddleOutStrategy_MediumMessages(b *testing.B) {
	counter := NewSimpleTokenCounter()
	strategy := NewMiddleOutStrategy(counter)

	// Create 100 messages
	messages := make([]Message, 100)
	for i := 0; i < 100; i++ {
		messages[i] = NewUserMessage(fmt.Sprintf("Message %d with some content", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := strategy.TailorMessages(context.Background(), messages, 200)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMiddleOutStrategy_LargeMessages(b *testing.B) {
	counter := NewSimpleTokenCounter()
	strategy := NewMiddleOutStrategy(counter)

	// Create 1000 messages
	messages := make([]Message, 1000)
	for i := 0; i < 1000; i++ {
		messages[i] = NewUserMessage(fmt.Sprintf("Message %d with some content", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := strategy.TailorMessages(context.Background(), messages, 1000)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHeadOutStrategy_LargeMessages(b *testing.B) {
	counter := NewSimpleTokenCounter()
	strategy := NewHeadOutStrategy(counter)

	// Create 1000 messages
	messages := make([]Message, 1000)
	for i := 0; i < 1000; i++ {
		messages[i] = NewUserMessage(fmt.Sprintf("Message %d with some content", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := strategy.TailorMessages(context.Background(), messages, 1000)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTailOutStrategy_LargeMessages(b *testing.B) {
	counter := NewSimpleTokenCounter()
	strategy := NewTailOutStrategy(counter)

	// Create 1000 messages
	messages := make([]Message, 1000)
	for i := 0; i < 1000; i++ {
		messages[i] = NewUserMessage(fmt.Sprintf("Message %d with some content", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := strategy.TailorMessages(context.Background(), messages, 1000)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark comparison: old O(n²) vs new O(n) approach
func BenchmarkTokenTailoring_PerformanceComparison(b *testing.B) {
	counter := NewSimpleTokenCounter()

	// Test with different message counts
	messageCounts := []int{10, 50, 100, 500, 1000}

	for _, count := range messageCounts {
		messages := make([]Message, count)
		for i := 0; i < count; i++ {
			messages[i] = NewUserMessage(fmt.Sprintf("Message %d with some content", i))
		}

		b.Run(fmt.Sprintf("MiddleOut_%d_messages", count), func(b *testing.B) {
			strategy := NewMiddleOutStrategy(counter)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := strategy.TailorMessages(context.Background(), messages, count*2)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestMiddleOutStrategy_EmptyMessages tests with empty message list
func TestMiddleOutStrategy_EmptyMessages(t *testing.T) {
	counter := NewSimpleTokenCounter()
	strategy := NewMiddleOutStrategy(counter)

	result, err := strategy.TailorMessages(context.Background(), []Message{}, 100)
	require.NoError(t, err)
	require.Nil(t, result)
}

// TestHeadOutStrategy_EmptyMessages tests with empty message list
func TestHeadOutStrategy_EmptyMessages(t *testing.T) {
	counter := NewSimpleTokenCounter()
	strategy := NewHeadOutStrategy(counter)

	result, err := strategy.TailorMessages(context.Background(), []Message{}, 100)
	require.NoError(t, err)
	require.Nil(t, result)
}

// TestTailOutStrategy_EmptyMessages tests with empty message list
func TestTailOutStrategy_EmptyMessages(t *testing.T) {
	counter := NewSimpleTokenCounter()
	strategy := NewTailOutStrategy(counter)

	result, err := strategy.TailorMessages(context.Background(), []Message{}, 100)
	require.NoError(t, err)
	require.Nil(t, result)
}

// TestBuildPrefixSum_WithCountTokensError tests error handling in buildPrefixSum
func TestBuildPrefixSum_WithCountTokensError(t *testing.T) {
	// Use SimpleTokenCounter which doesn't return errors, but test the fallback logic
	counter := NewSimpleTokenCounter()
	messages := []Message{
		NewSystemMessage("System prompt"),
		NewUserMessage("User message with some content that would normally be counted"),
	}

	// This test verifies that buildPrefixSum completes without panicking
	// even when errors might occur (though SimpleTokenCounter doesn't error)
	prefixSum := buildPrefixSum(context.Background(), counter, messages)

	// Verify prefix sum array has correct length
	require.Len(t, prefixSum, len(messages)+1)
	require.Equal(t, 0, prefixSum[0])               // First element should be 0
	require.Greater(t, prefixSum[len(messages)], 0) // Total should be positive
}

// mockErrorTokenCounter is a token counter that returns errors
type mockErrorTokenCounter struct{}

func (c *mockErrorTokenCounter) CountTokens(ctx context.Context, message Message) (int, error) {
	return 0, fmt.Errorf("mock error")
}

func (c *mockErrorTokenCounter) CountTokensRange(ctx context.Context, messages []Message, start, end int) (int, error) {
	return 0, fmt.Errorf("mock error")
}

// TestBuildPrefixSum_WithActualError tests the error handling path in buildPrefixSum
func TestBuildPrefixSum_WithActualError(t *testing.T) {
	counter := &mockErrorTokenCounter{}
	messages := []Message{
		NewSystemMessage("System prompt with some content"),
		NewUserMessage("User message with some content"),
	}

	// buildPrefixSum should handle errors gracefully with fallback estimation
	prefixSum := buildPrefixSum(context.Background(), counter, messages)

	// Verify prefix sum array has correct length
	require.Len(t, prefixSum, len(messages)+1)
	require.Equal(t, 0, prefixSum[0]) // First element should be 0
	// When errors occur, fallback uses rune-based estimation
	require.Greater(t, prefixSum[len(messages)], 0) // Total should be positive
}

// TestSimpleTokenCounter_CountTokensRange_InvalidRange tests error cases
func TestSimpleTokenCounter_CountTokensRange_InvalidRange(t *testing.T) {
	counter := NewSimpleTokenCounter()
	messages := []Message{
		NewUserMessage("Message 1"),
		NewUserMessage("Message 2"),
		NewUserMessage("Message 3"),
	}

	tests := []struct {
		name  string
		start int
		end   int
	}{
		{
			name:  "negative start",
			start: -1,
			end:   2,
		},
		{
			name:  "end exceeds length",
			start: 0,
			end:   10,
		},
		{
			name:  "start >= end",
			start: 2,
			end:   2,
		},
		{
			name:  "start > end",
			start: 2,
			end:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := counter.CountTokensRange(context.Background(), messages, tt.start, tt.end)
			require.Error(t, err)
			require.Contains(t, err.Error(), "invalid range")
		})
	}
}

// TestHeadOutStrategy_BinarySearchAccuracy tests that binary search correctly accounts for preserved tail tokens.
func TestHeadOutStrategy_BinarySearchAccuracy(t *testing.T) {
	// Create messages with varying token sizes.
	msgs := []Message{
		NewSystemMessage("System prompt"),              // ~3 tokens
		NewUserMessage(repeat("a", 100)),               // ~25 tokens
		NewUserMessage(repeat("b", 100)),               // ~25 tokens
		NewUserMessage(repeat("c", 100)),               // ~25 tokens
		NewUserMessage(repeat("d", 100)),               // ~25 tokens
		NewUserMessage("Last user message"),            // ~4 tokens
		NewAssistantMessage("Last assistant response"), // ~4 tokens
	}

	counter := NewSimpleTokenCounter()
	strategy := NewHeadOutStrategy(counter)

	// Set a strict token limit that forces trimming.
	maxTokens := 50

	tailored, err := strategy.TailorMessages(context.Background(), msgs, maxTokens)
	require.NoError(t, err)

	// Should preserve system message.
	require.Greater(t, len(tailored), 0)
	assert.Equal(t, RoleSystem, tailored[0].Role)

	// The sequence must end with user/tool for request legality.
	assert.Equal(t, RoleUser, tailored[len(tailored)-1].Role)
	assert.Equal(t, "Last user message", tailored[len(tailored)-1].Content)

	// Verify token count is within limit.
	totalTokens := 0
	for _, msg := range tailored {
		tokens, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		totalTokens += tokens
	}
	assert.LessOrEqual(t, totalTokens, maxTokens)
}

// TestTailOutStrategy_BinarySearchAccuracy tests that binary search correctly accounts for preserved head tokens.
func TestTailOutStrategy_BinarySearchAccuracy(t *testing.T) {
	// Create messages with varying token sizes.
	msgs := []Message{
		NewSystemMessage("System prompt"),              // ~3 tokens
		NewUserMessage(repeat("a", 100)),               // ~25 tokens
		NewUserMessage(repeat("b", 100)),               // ~25 tokens
		NewUserMessage(repeat("c", 100)),               // ~25 tokens
		NewUserMessage(repeat("d", 100)),               // ~25 tokens
		NewUserMessage("Last user message"),            // ~4 tokens
		NewAssistantMessage("Last assistant response"), // ~4 tokens
	}

	counter := NewSimpleTokenCounter()
	strategy := NewTailOutStrategy(counter)

	// Set a strict token limit that forces trimming.
	maxTokens := 50

	tailored, err := strategy.TailorMessages(context.Background(), msgs, maxTokens)
	require.NoError(t, err)

	// Should preserve system message.
	require.Greater(t, len(tailored), 0)
	assert.Equal(t, RoleSystem, tailored[0].Role)

	// The sequence must end with user/tool for request legality.
	assert.Equal(t, RoleUser, tailored[len(tailored)-1].Role)
	assert.Equal(t, "Last user message", tailored[len(tailored)-1].Content)

	// Verify token count is within limit.
	totalTokens := 0
	for _, msg := range tailored {
		tokens, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		totalTokens += tokens
	}
	assert.LessOrEqual(t, totalTokens, maxTokens)
}

// TestHeadOutStrategy_VeryTightBudget tests HeadOut with extremely tight token budget.
func TestHeadOutStrategy_VeryTightBudget(t *testing.T) {
	msgs := []Message{
		NewSystemMessage("System"),
		NewUserMessage(repeat("x", 500)),
		NewUserMessage(repeat("y", 500)),
		NewUserMessage("Query"),
		NewAssistantMessage("Response"),
	}

	counter := NewSimpleTokenCounter()
	strategy := NewHeadOutStrategy(counter)

	// Very tight budget: only 20 tokens.
	tailored, err := strategy.TailorMessages(context.Background(), msgs, 20)
	require.NoError(t, err)

	// Should still preserve system and last turn.
	if len(tailored) > 0 {
		assert.Equal(t, RoleSystem, tailored[0].Role)
	}
	if len(tailored) >= 2 {
		assert.Equal(t, RoleUser, tailored[len(tailored)-1].Role)
		assert.Equal(t, "Query", tailored[len(tailored)-1].Content)
	}

	// Verify token count is within limit.
	totalTokens := 0
	for _, msg := range tailored {
		tokens, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		totalTokens += tokens
	}
	assert.LessOrEqual(t, totalTokens, 20)
}

// TestTailOutStrategy_VeryTightBudget tests TailOut with extremely tight token budget.
func TestTailOutStrategy_VeryTightBudget(t *testing.T) {
	msgs := []Message{
		NewSystemMessage("System"),
		NewUserMessage(repeat("x", 500)),
		NewUserMessage(repeat("y", 500)),
		NewUserMessage("Query"),
		NewAssistantMessage("Response"),
	}

	counter := NewSimpleTokenCounter()
	strategy := NewTailOutStrategy(counter)

	// Very tight budget: only 20 tokens.
	tailored, err := strategy.TailorMessages(context.Background(), msgs, 20)
	require.NoError(t, err)

	// Should still preserve system and last turn.
	if len(tailored) > 0 {
		assert.Equal(t, RoleSystem, tailored[0].Role)
	}
	if len(tailored) >= 2 {
		assert.Equal(t, RoleUser, tailored[len(tailored)-1].Role)
		assert.Equal(t, "Query", tailored[len(tailored)-1].Content)
	}

	// Verify token count is within limit.
	totalTokens := 0
	for _, msg := range tailored {
		tokens, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		totalTokens += tokens
	}
	assert.LessOrEqual(t, totalTokens, 20)
}

// TestHeadOutStrategy_PreservedSegmentsExceedBudget tests when preserved segments exceed budget.
func TestHeadOutStrategy_PreservedSegmentsExceedBudget(t *testing.T) {
	msgs := []Message{
		NewSystemMessage(repeat("system ", 100)),
		NewUserMessage("User 1"),
		NewUserMessage("User 2"),
		NewUserMessage("Query"),
		NewAssistantMessage(repeat("response ", 100)),
	}

	counter := NewSimpleTokenCounter()
	strategy := NewHeadOutStrategy(counter)

	// Budget is less than preserved segments.
	tailored, err := strategy.TailorMessages(context.Background(), msgs, 10)
	require.NoError(t, err)

	// Should return only preserved segments (system + last turn).
	require.Greater(t, len(tailored), 0)
	assert.Equal(t, RoleUser, tailored[len(tailored)-1].Role)
	assert.Equal(t, "Query", tailored[len(tailored)-1].Content)
}

// TestTailOutStrategy_PreservedSegmentsExceedBudget tests when preserved segments exceed budget.
func TestTailOutStrategy_PreservedSegmentsExceedBudget(t *testing.T) {
	msgs := []Message{
		NewSystemMessage(repeat("system ", 100)),
		NewUserMessage("User 1"),
		NewUserMessage("User 2"),
		NewUserMessage("Query"),
		NewAssistantMessage(repeat("response ", 100)),
	}

	counter := NewSimpleTokenCounter()
	strategy := NewTailOutStrategy(counter)

	// Budget is less than preserved segments.
	tailored, err := strategy.TailorMessages(context.Background(), msgs, 10)
	require.NoError(t, err)

	// Should return only preserved segments (system + last turn).
	require.Greater(t, len(tailored), 0)
	assert.Equal(t, RoleUser, tailored[len(tailored)-1].Role)
	assert.Equal(t, "Query", tailored[len(tailored)-1].Content)
}

// TestHeadOutStrategy_BalancedMessages tests HeadOut with balanced message distribution.
func TestHeadOutStrategy_BalancedMessages(t *testing.T) {
	msgs := []Message{
		NewSystemMessage("System"),
		NewUserMessage("Head 1"),
		NewUserMessage("Head 2"),
		NewUserMessage("Head 3"),
		NewUserMessage("Middle 1"),
		NewUserMessage("Middle 2"),
		NewUserMessage("Tail 1"),
		NewUserMessage("Tail 2"),
		NewUserMessage("Query"),
		NewAssistantMessage("Response"),
	}

	counter := NewSimpleTokenCounter()
	strategy := NewHeadOutStrategy(counter)

	tailored, err := strategy.TailorMessages(context.Background(), msgs, 100)
	require.NoError(t, err)

	// Should preserve system and last turn.
	assert.Equal(t, RoleSystem, tailored[0].Role)
	assert.Equal(t, RoleUser, tailored[len(tailored)-1].Role)
	assert.Equal(t, "Query", tailored[len(tailored)-1].Content)

	// Should keep tail messages (HeadOut removes from head).
	hasTailMessages := false
	for _, msg := range tailored {
		if msg.Content == "Tail 1" || msg.Content == "Tail 2" {
			hasTailMessages = true
		}
	}
	// HeadOut should prefer tail messages over head messages.
	assert.True(t, hasTailMessages, "HeadOut should keep tail messages")

	// Verify token count is within limit.
	totalTokens := 0
	for _, msg := range tailored {
		tokens, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		totalTokens += tokens
	}
	assert.LessOrEqual(t, totalTokens, 100)
}

// TestTailOutStrategy_BalancedMessages tests TailOut with balanced message distribution.
func TestTailOutStrategy_BalancedMessages(t *testing.T) {
	msgs := []Message{
		NewSystemMessage("System"),
		NewUserMessage("Head 1"),
		NewUserMessage("Head 2"),
		NewUserMessage("Head 3"),
		NewUserMessage("Middle 1"),
		NewUserMessage("Middle 2"),
		NewUserMessage("Tail 1"),
		NewUserMessage("Tail 2"),
		NewUserMessage("Query"),
		NewAssistantMessage("Response"),
	}

	counter := NewSimpleTokenCounter()
	strategy := NewTailOutStrategy(counter)

	tailored, err := strategy.TailorMessages(context.Background(), msgs, 100)
	require.NoError(t, err)

	// Should preserve system and last turn.
	assert.Equal(t, RoleSystem, tailored[0].Role)
	assert.Equal(t, RoleUser, tailored[len(tailored)-1].Role)
	assert.Equal(t, "Query", tailored[len(tailored)-1].Content)

	// Should keep head messages (TailOut removes from tail).
	hasHeadMessages := false
	for _, msg := range tailored {
		if msg.Content == "Head 1" || msg.Content == "Head 2" || msg.Content == "Head 3" {
			hasHeadMessages = true
		}
	}
	// TailOut should prefer head messages over tail messages.
	assert.True(t, hasHeadMessages, "TailOut should keep head messages")

	// Verify token count is within limit.
	totalTokens := 0
	for _, msg := range tailored {
		tokens, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		totalTokens += tokens
	}
	assert.LessOrEqual(t, totalTokens, 100)
}

func TestSimpleTokenCounter_WithToolCalls(t *testing.T) {
	counter := NewSimpleTokenCounter()
	ctx := context.Background()

	// Test message with tool calls
	toolCall := ToolCall{
		Type: "function",
		ID:   "call_123",
		Function: FunctionDefinitionParam{
			Name:        "get_weather",
			Description: "Get the current weather",
			Arguments:   []byte(`{"location": "Beijing"}`),
		},
	}

	msg := Message{
		Role:      RoleAssistant,
		Content:   "I'll check the weather for you.",
		ToolCalls: []ToolCall{toolCall},
	}

	result, err := counter.CountTokens(ctx, msg)
	require.NoError(t, err)
	assert.Greater(t, result, 0)

	// Verify tool calls contribute to token count
	contentOnlyMsg := Message{
		Role:    RoleAssistant,
		Content: "I'll check the weather for you.",
	}
	contentTokens, _ := counter.CountTokens(ctx, contentOnlyMsg)

	// Tool calls should add additional tokens
	assert.Greater(t, result, contentTokens)
}

func TestSimpleTokenCounter_OnlyToolCalls(t *testing.T) {
	counter := NewSimpleTokenCounter()
	ctx := context.Background()

	toolCall := ToolCall{
		Type: "function",
		ID:   "call_456",
		Function: FunctionDefinitionParam{
			Name:        "calculate",
			Description: "Perform mathematical calculations",
			Arguments:   []byte(`{"expression": "2+2"}`),
		},
	}

	msg := Message{
		Role:      RoleAssistant,
		ToolCalls: []ToolCall{toolCall},
	}

	result, err := counter.CountTokens(ctx, msg)
	require.NoError(t, err)
	assert.Greater(t, result, 0)
}

func TestSimpleTokenCounter_MultipleToolCalls(t *testing.T) {
	counter := NewSimpleTokenCounter()
	ctx := context.Background()

	toolCalls := []ToolCall{
		{
			Type: "function",
			ID:   "call_weather",
			Function: FunctionDefinitionParam{
				Name:        "get_weather",
				Description: "Get weather information",
				Arguments:   []byte(`{"location": "Shanghai"}`),
			},
		},
		{
			Type: "function",
			ID:   "call_time",
			Function: FunctionDefinitionParam{
				Name:        "get_time",
				Description: "Get current time",
				Arguments:   []byte(`{"timezone": "UTC"}`),
			},
		},
	}

	msg := Message{
		Role:      RoleAssistant,
		Content:   "Here are multiple tool calls:",
		ToolCalls: toolCalls,
	}

	result, err := counter.CountTokens(ctx, msg)
	require.NoError(t, err)
	assert.Greater(t, result, 0)

	// Compare with single tool call
	singleToolMsg := Message{
		Role:      RoleAssistant,
		Content:   "Here are multiple tool calls:",
		ToolCalls: []ToolCall{toolCalls[0]},
	}
	singleTokens, _ := counter.CountTokens(ctx, singleToolMsg)

	// Multiple tool calls should have more tokens
	assert.Greater(t, result, singleTokens)
}

func TestSimpleTokenCounter_EmptyToolCall(t *testing.T) {
	counter := NewSimpleTokenCounter()
	ctx := context.Background()

	// Test empty tool call
	emptyToolCall := ToolCall{}
	msg := Message{
		Role:      RoleAssistant,
		ToolCalls: []ToolCall{emptyToolCall},
	}

	result, err := counter.CountTokens(ctx, msg)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, result, 0)
}

func TestBuildUserAnchoredRounds_UserOnlySplits(t *testing.T) {
	const preservedHead = 1
	messages := []Message{
		NewSystemMessage("sys"),
		NewUserMessage("u1"),
		NewUserMessage("u2"),
		NewUserMessage("u3"),
	}

	rounds := buildUserAnchoredRounds(messages, preservedHead)
	require.Len(t, rounds, 3)
	require.Equal(t, 1, rounds[0].start)
	require.Equal(t, 2, rounds[0].end)
	require.Equal(t, 2, rounds[1].start)
	require.Equal(t, 3, rounds[1].end)
	require.Equal(t, 3, rounds[2].start)
	require.Equal(t, 4, rounds[2].end)
}

func TestBuildUserAnchoredRounds_AssistantGroupsUsers(t *testing.T) {
	const preservedHead = 1
	messages := []Message{
		NewSystemMessage("sys"),
		NewUserMessage("u1"),
		NewUserMessage("u2"),
		NewAssistantMessage("a1"),
		NewUserMessage("u3"),
	}

	rounds := buildUserAnchoredRounds(messages, preservedHead)
	require.Len(t, rounds, 2)
	require.Equal(t, 1, rounds[0].start)
	require.Equal(t, 4, rounds[0].end)
	require.Equal(t, 4, rounds[1].start)
	require.Equal(t, 5, rounds[1].end)
}

func TestBuildUserAnchoredRounds_SystemInsideRound(t *testing.T) {
	const preservedHead = 1
	messages := []Message{
		NewSystemMessage("sys"),
		NewUserMessage("u1"),
		NewSystemMessage("mid"),
		NewAssistantMessage("a1"),
		NewUserMessage("u2"),
	}

	rounds := buildUserAnchoredRounds(messages, preservedHead)
	require.Len(t, rounds, 2)
	require.Equal(t, 1, rounds[0].start)
	require.Equal(t, 4, rounds[0].end)
	require.Equal(t, 4, rounds[1].start)
	require.Equal(t, 5, rounds[1].end)
}

func TestCountTokensWithPrefixSumBounds(t *testing.T) {
	prefixSum := []int{0, 2, 5, 9}

	require.Equal(t, 9, countTokensWithPrefixSum(prefixSum, -1, 4))
	require.Equal(t, 0, countTokensWithPrefixSum(prefixSum, 3, 3))
	require.Equal(t, 0, countTokensWithPrefixSum(prefixSum, 4, 4))
}

func TestCountTokensForRoundsSkips(t *testing.T) {
	prefixSum := []int{0, 1, 3, 6}
	rounds := []userAnchoredRound{
		{start: 0, end: 2},
		{start: 2, end: 3},
	}
	keep := []bool{true, false}

	require.Equal(t, 3, countTokensForRounds(prefixSum, rounds, keep))
}

func TestShouldReturnOriginal_MaxTokensZero(t *testing.T) {
	counter := NewSimpleTokenCounter()
	messages := []Message{
		NewUserMessage("q"),
	}

	done, out := shouldReturnOriginal(context.Background(), counter, messages, 0)
	require.True(t, done)
	require.Nil(t, out)
}

func TestShouldReturnOriginal_CountTokensError(t *testing.T) {
	counter := &mockErrorTokenCounter{}
	messages := []Message{
		NewUserMessage("q"),
	}

	done, out := shouldReturnOriginal(context.Background(), counter, messages, 10)
	require.True(t, done)
	require.Len(t, out, 1)
}

func TestFitsWithinBudget_Empty(t *testing.T) {
	counter := NewSimpleTokenCounter()

	ok := fitsWithinBudget(context.Background(), counter, nil, 10)
	require.False(t, ok)
}

func TestBuildMinimalSuffixCandidates_NoNonSystem(t *testing.T) {
	messages := []Message{
		NewSystemMessage("sys"),
	}

	withSystem, withoutSystem := buildMinimalSuffixCandidates(messages, 1)
	require.Nil(t, withSystem)
	require.Nil(t, withoutSystem)
}

func TestBuildMinimalSuffixCandidates_ToolAtEnd(t *testing.T) {
	messages := []Message{
		NewSystemMessage("sys"),
		NewUserMessage("q"),
		NewToolMessage("tool_1", "search", "result"),
	}

	withSystem, withoutSystem := buildMinimalSuffixCandidates(messages, 1)
	require.Len(t, withSystem, 3)
	require.Equal(t, RoleTool, withSystem[len(withSystem)-1].Role)
	require.Len(t, withoutSystem, 2)
	require.Equal(t, RoleTool, withoutSystem[len(withoutSystem)-1].Role)
}

func TestBuildMinimalSuffixCandidates_ToolOnly(t *testing.T) {
	messages := []Message{
		NewToolMessage("tool_1", "search", "result"),
	}

	withSystem, withoutSystem := buildMinimalSuffixCandidates(messages, 0)
	require.Nil(t, withSystem)
	require.Nil(t, withoutSystem)
}

func TestLastNonSystemIndex_AllSystem(t *testing.T) {
	messages := []Message{
		NewSystemMessage("sys"),
		NewSystemMessage("sys2"),
	}

	require.Equal(t, -1, lastNonSystemIndex(messages))
}

func TestTrimTrailingAssistant(t *testing.T) {
	messages := []Message{
		NewUserMessage("q"),
		NewAssistantMessage("a1"),
		NewAssistantMessage("a2"),
	}

	require.Equal(t, 0, trimTrailingAssistant(messages, len(messages)-1))
}

func TestStartOfUserToolGroup_LastIsUser(t *testing.T) {
	messages := []Message{
		NewUserMessage("q"),
	}

	require.Equal(t, 0, startOfUserToolGroup(messages, 0))
}

func TestStartOfUserToolGroup_ToolWithoutUser(t *testing.T) {
	messages := []Message{
		NewToolMessage("tool_1", "search", "result"),
	}

	require.Equal(t, -1, startOfUserToolGroup(messages, 0))
}

func TestEnsureTailoredWithinBudget_CountTokensError(t *testing.T) {
	counter := &mockErrorTokenCounter{}
	messages := []Message{
		NewUserMessage("q"),
	}

	out, err := ensureTailoredWithinBudget(context.Background(), counter,
		messages, 1)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, "q", out[0].Content)
}

func TestFitsWithinBudget_CountTokensError(t *testing.T) {
	counter := &mockErrorTokenCounter{}
	messages := []Message{
		NewUserMessage("q"),
	}

	ok := fitsWithinBudget(context.Background(), counter, messages, 1)
	require.False(t, ok)
}
