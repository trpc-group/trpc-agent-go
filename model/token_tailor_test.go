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

// TestSimpleTokenCounter_CountTokens_DetailedCoverage tests all code paths in CountTokens function
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

	t.Run("basic content ensures minimum 1 token", func(t *testing.T) {
		msg := Message{
			Role:    RoleUser,
			Content: "Hi", // 2 runes / 4 = 0 tokens, but max(0, 1) = 1
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 1, result) // max(0, 1) = 1
	})

	t.Run("content with reasoning content", func(t *testing.T) {
		msg := Message{
			Role:             RoleAssistant,
			Content:          "Answer",                     // 6 runes / 4 = 1 token
			ReasoningContent: "Let me think about this...", // 26 runes / 4 = 6 tokens
		}
		result, err := counter.CountTokens(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, 7, result) // max(1 + 6, 1) = 7
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
		textPart1 := "Additional info" // 15 runes / 4 = 3 tokens
		textPart2 := "More details"    // 12 runes / 4 = 3 tokens
		msg := Message{
			Role:             RoleAssistant,
			Content:          "Main answer",      // 11 runes / 4 = 2 tokens
			ReasoningContent: "Thinking process", // 16 runes / 4 = 4 tokens
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
		assert.Equal(t, 12, result) // max(2 + 4 + 3 + 3, 1) = 12
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

	// Should preserve last turn (last 2 messages).
	assert.Equal(t, "Question 5", tailored[len(tailored)-2].Content)
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

// TestCalculatePreservedTailCount tests the shared calculatePreservedTailCount function.
func TestCalculatePreservedTailCount(t *testing.T) {
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
				NewSystemMessage("You are a helpful assistant."),
			},
			expected: 1,
		},
		{
			name: "user-assistant pair",
			messages: []Message{
				NewSystemMessage("You are a helpful assistant."),
				NewUserMessage("Hello"),
				NewAssistantMessage("Hi there!"),
			},
			expected: 2, // user + assistant
		},
		{
			name: "user-assistant-tool sequence",
			messages: []Message{
				NewSystemMessage("You are a helpful assistant."),
				NewUserMessage("What's the weather?"),
				NewAssistantMessage("Let me check."),
				NewToolMessage("tool_1", "weather", "Sunny, 75°F"),
			},
			expected: 3, // user + assistant + tool
		},
		{
			name: "multiple turns with tool at end",
			messages: []Message{
				NewSystemMessage("You are a helpful assistant."),
				NewUserMessage("First question"),
				NewAssistantMessage("First answer"),
				NewUserMessage("What's the weather?"),
				NewAssistantMessage("Let me check."),
				NewToolMessage("tool_1", "weather", "Sunny"),
			},
			expected: 3, // last user + assistant + tool
		},
		{
			name: "assistant without preceding user",
			messages: []Message{
				NewSystemMessage("You are a helpful assistant."),
				NewAssistantMessage("Hello!"),
			},
			expected: 1, // only assistant
		},
		{
			name: "user without assistant",
			messages: []Message{
				NewSystemMessage("You are a helpful assistant."),
				NewUserMessage("Hello"),
			},
			expected: 1, // only last message (user)
		},
		{
			name: "tool between user and assistant",
			messages: []Message{
				NewSystemMessage("You are a helpful assistant."),
				NewUserMessage("Hello"),
				NewToolMessage("tool_1", "search", "Result"),
				NewAssistantMessage("Based on the result..."),
			},
			expected: 3, // user + tool + assistant (from user to end, tool is skipped in search but included in result)
		},
		{
			name: "multiple tools after user",
			messages: []Message{
				NewSystemMessage("You are a helpful assistant."),
				NewUserMessage("Search for something"),
				NewToolMessage("tool_1", "search", "Result 1"),
				NewToolMessage("tool_2", "search", "Result 2"),
				NewAssistantMessage("Here are the results..."),
			},
			expected: 4, // user + 2 tools + assistant
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculatePreservedTailCount(tt.messages)
			require.Equal(t, tt.expected, result, "Expected %d preserved messages, got %d", tt.expected, result)

			// Verify the actual messages that would be preserved.
			if result > 0 && len(tt.messages) > 0 {
				preservedMessages := tt.messages[len(tt.messages)-result:]
				t.Logf("Preserved messages: %d", len(preservedMessages))
				for i, msg := range preservedMessages {
					t.Logf("  [%d] %s: %s", i, msg.Role, truncateContent(msg.Content, 50))
				}
			}
		})
	}
}

// Helper function to truncate content for logging.
func truncateContent(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

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

// TestBuildPreservedOnlyResult tests the shared buildPreservedOnlyResult function.
func TestBuildPreservedOnlyResult(t *testing.T) {
	tests := []struct {
		name          string
		messages      []Message
		preservedHead int
		preservedTail int
		expected      []Message
		description   string
	}{
		{
			name: "preserve head and tail",
			messages: []Message{
				NewSystemMessage("System"),
				NewUserMessage("User 1"),
				NewUserMessage("User 2"),
				NewUserMessage("User 3"),
				NewUserMessage("User 4"),
			},
			preservedHead: 1,
			preservedTail: 2,
			expected: []Message{
				NewSystemMessage("System"),
				NewUserMessage("User 3"),
				NewUserMessage("User 4"),
			},
			description: "Should preserve system message and last 2 messages",
		},
		{
			name: "preserve only head",
			messages: []Message{
				NewSystemMessage("System"),
				NewUserMessage("User 1"),
				NewUserMessage("User 2"),
			},
			preservedHead: 1,
			preservedTail: 0,
			expected: []Message{
				NewSystemMessage("System"),
			},
			description: "Should preserve only system message",
		},
		{
			name: "preserve only tail",
			messages: []Message{
				NewSystemMessage("System"),
				NewUserMessage("User 1"),
				NewUserMessage("User 2"),
			},
			preservedHead: 0,
			preservedTail: 2,
			expected: []Message{
				NewUserMessage("User 1"),
				NewUserMessage("User 2"),
			},
			description: "Should preserve only last 2 messages",
		},
		{
			name: "remove leading tool message",
			messages: []Message{
				NewToolMessage("tool_1", "test", "Result"),
				NewUserMessage("User 1"),
				NewUserMessage("User 2"),
			},
			preservedHead: 0,
			preservedTail: 3,
			expected: []Message{
				NewUserMessage("User 1"),
				NewUserMessage("User 2"),
			},
			description: "Should remove leading tool message",
		},
		{
			name: "tool message in head, keep in tail",
			messages: []Message{
				NewSystemMessage("System"),
				NewUserMessage("User 1"),
				NewToolMessage("tool_1", "test", "Result"),
				NewUserMessage("User 2"),
			},
			preservedHead: 1,
			preservedTail: 2,
			expected: []Message{
				NewSystemMessage("System"),
				NewToolMessage("tool_1", "test", "Result"),
				NewUserMessage("User 2"),
			},
			description: "Tool message in tail should be preserved",
		},
		{
			name: "all head preserved leads with tool",
			messages: []Message{
				NewToolMessage("tool_1", "test", "Result"),
				NewUserMessage("User 1"),
				NewUserMessage("User 2"),
			},
			preservedHead: 3,
			preservedTail: 0,
			expected: []Message{
				NewUserMessage("User 1"),
				NewUserMessage("User 2"),
			},
			description: "Leading tool message should be removed even when all head is preserved",
		},
		{
			name: "empty result",
			messages: []Message{
				NewUserMessage("User 1"),
				NewUserMessage("User 2"),
			},
			preservedHead: 0,
			preservedTail: 0,
			expected:      []Message{},
			description:   "Empty preservation should return empty result",
		},
		{
			name: "single tool message only",
			messages: []Message{
				NewToolMessage("tool_1", "test", "Result"),
			},
			preservedHead: 0,
			preservedTail: 1,
			expected:      []Message{},
			description:   "Single tool message should be removed, resulting in empty list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPreservedOnlyResult(tt.messages, tt.preservedHead, tt.preservedTail)

			require.Equal(t, len(tt.expected), len(result),
				"%s: expected %d messages, got %d", tt.description, len(tt.expected), len(result))

			for i := range result {
				assert.Equal(t, tt.expected[i].Role, result[i].Role,
					"%s: message[%d] role mismatch", tt.description, i)
				assert.Equal(t, tt.expected[i].Content, result[i].Content,
					"%s: message[%d] content mismatch", tt.description, i)
			}
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

// TestBuildPreservedOnlyResult_EdgeCases tests edge cases in buildPreservedOnlyResult
func TestBuildPreservedOnlyResult_EdgeCases(t *testing.T) {
	t.Run("preserved segments exceed message count", func(t *testing.T) {
		messages := []Message{
			NewSystemMessage("System"),
			NewUserMessage("User"),
		}

		// Request more tail messages than available after head
		result := buildPreservedOnlyResult(messages, 1, 5)
		require.NotNil(t, result)
		// Should adjust preservedTail to not exceed available messages
		require.LessOrEqual(t, len(result), len(messages))
	})

	t.Run("starts with tool message", func(t *testing.T) {
		messages := []Message{
			NewToolMessage("tool1", "func1", "result"),
			NewUserMessage("User"),
		}

		result := buildPreservedOnlyResult(messages, 0, 2)
		// Tool message at start should be removed
		require.Len(t, result, 1)
		require.Equal(t, RoleUser, result[0].Role)
	})
}

// TestCalculatePreservedTailCount_EdgeCases tests edge cases in calculatePreservedTailCount
func TestCalculatePreservedTailCount_EdgeCases(t *testing.T) {
	t.Run("empty messages", func(t *testing.T) {
		count := calculatePreservedTailCount([]Message{})
		require.Equal(t, 0, count)
	})

	t.Run("only system message", func(t *testing.T) {
		messages := []Message{
			NewSystemMessage("System"),
		}
		count := calculatePreservedTailCount(messages)
		require.Equal(t, 1, count) // Should preserve last message
	})

	t.Run("system then user message", func(t *testing.T) {
		messages := []Message{
			NewSystemMessage("System"),
			NewUserMessage("User"),
		}
		count := calculatePreservedTailCount(messages)
		require.Equal(t, 1, count) // Should preserve last message (no assistant found)
	})

	t.Run("assistant without preceding user", func(t *testing.T) {
		messages := []Message{
			NewSystemMessage("System"),
			NewAssistantMessage("Assistant"),
		}
		count := calculatePreservedTailCount(messages)
		require.Equal(t, 1, count) // Should preserve assistant message
	})

	t.Run("assistant at start", func(t *testing.T) {
		messages := []Message{
			NewAssistantMessage("Assistant"),
		}
		count := calculatePreservedTailCount(messages)
		require.Equal(t, 1, count) // Should preserve all messages
	})
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

	// Should preserve last turn (last 2 messages).
	assert.Equal(t, "Last user message", tailored[len(tailored)-2].Content)
	assert.Equal(t, "Last assistant response", tailored[len(tailored)-1].Content)

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

	// Should preserve last turn (last 2 messages).
	assert.Equal(t, "Last user message", tailored[len(tailored)-2].Content)
	assert.Equal(t, "Last assistant response", tailored[len(tailored)-1].Content)

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
		assert.Equal(t, "Query", tailored[len(tailored)-2].Content)
		assert.Equal(t, "Response", tailored[len(tailored)-1].Content)
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
		assert.Equal(t, "Query", tailored[len(tailored)-2].Content)
		assert.Equal(t, "Response", tailored[len(tailored)-1].Content)
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
	assert.Equal(t, RoleSystem, tailored[0].Role)
	assert.Equal(t, RoleAssistant, tailored[len(tailored)-1].Role)
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
	assert.Equal(t, RoleSystem, tailored[0].Role)
	assert.Equal(t, RoleAssistant, tailored[len(tailored)-1].Role)
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
	assert.Equal(t, "Query", tailored[len(tailored)-2].Content)
	assert.Equal(t, "Response", tailored[len(tailored)-1].Content)

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
	assert.Equal(t, "Query", tailored[len(tailored)-2].Content)
	assert.Equal(t, "Response", tailored[len(tailored)-1].Content)

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
