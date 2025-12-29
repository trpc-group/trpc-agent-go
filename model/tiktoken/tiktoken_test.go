//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tiktoken

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tiktoken-go/tokenizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// mockCodec implements Codec interface for testing error conditions
type mockCodec struct {
	shouldFail bool
}

func (m *mockCodec) GetName() string {
	return "mock"
}

func (m *mockCodec) Count(text string) (int, error) {
	if m.shouldFail {
		return 0, errors.New("mock count error")
	}
	return len(text), nil
}

func (m *mockCodec) Encode(text string) ([]uint, []string, error) {
	if m.shouldFail {
		return nil, nil, errors.New("mock encoding error")
	}
	// Return a simple tokenization: 1 token per character
	tokens := make([]uint, len(text))
	for i := range tokens {
		tokens[i] = uint(text[i])
	}
	return tokens, nil, nil
}

func (m *mockCodec) Decode(tokens []uint) (string, error) {
	if m.shouldFail {
		return "", errors.New("mock decoding error")
	}
	// Simple reverse tokenization
	var result []byte
	for _, token := range tokens {
		result = append(result, byte(token))
	}
	return string(result), nil
}

func TestTiktokenCounter_CountTokens(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}
	msg := model.NewUserMessage("Hello, world!")
	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, used, 0)
}

func TestTiktokenCounter_ModelFallback(t *testing.T) {
	counter, err := New("unknown-model-name-xyz")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}
	msg := model.NewUserMessage("alpha beta gamma")
	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, used, 0)
}

func TestTiktokenCounter_ContentPartsAndReasoning(t *testing.T) {
	counter, err := New("gpt-4")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}
	text := "part text"
	msg := model.Message{
		Role:             model.RoleUser,
		Content:          "main",
		ReasoningContent: "think",
		ContentParts:     []model.ContentPart{{Type: model.ContentTypeText, Text: &text}},
	}
	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, used, 0)
}

func TestTiktokenCounter_EmptyMessage(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}
	msg := model.Message{}
	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Equal(t, 0, used)
}

func TestTiktokenCounter_CountTokensRange(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}

	messages := []model.Message{
		model.NewUserMessage("Hello"),
		model.NewUserMessage("World"),
		model.NewUserMessage("Test"),
	}

	t.Run("valid range - all messages", func(t *testing.T) {
		used, err := counter.CountTokensRange(context.Background(), messages, 0, 3)
		require.NoError(t, err)
		require.Greater(t, used, 0)
	})

	t.Run("valid range - subset", func(t *testing.T) {
		used, err := counter.CountTokensRange(context.Background(), messages, 1, 3)
		require.NoError(t, err)
		require.Greater(t, used, 0)
	})

	t.Run("valid range - single message", func(t *testing.T) {
		used, err := counter.CountTokensRange(context.Background(), messages, 0, 1)
		require.NoError(t, err)
		require.Greater(t, used, 0)
	})

	t.Run("invalid range - start < 0", func(t *testing.T) {
		_, err := counter.CountTokensRange(context.Background(), messages, -1, 2)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid range")
	})

	t.Run("invalid range - end > len", func(t *testing.T) {
		_, err := counter.CountTokensRange(context.Background(), messages, 0, 5)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid range")
	})

	t.Run("invalid range - start >= end", func(t *testing.T) {
		_, err := counter.CountTokensRange(context.Background(), messages, 2, 1)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid range")
	})

	t.Run("invalid range - start == end", func(t *testing.T) {
		_, err := counter.CountTokensRange(context.Background(), messages, 1, 1)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid range")
	})
}

func TestTiktokenCounter_OnlyReasoningContent(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}
	msg := model.Message{
		Role:             model.RoleAssistant,
		ReasoningContent: "Let me think about this carefully",
	}
	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, used, 0)
}

func TestTiktokenCounter_OnlyContentParts(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}
	text1 := "First part"
	text2 := "Second part"
	msg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text1},
			{Type: model.ContentTypeText, Text: &text2},
		},
	}
	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, used, 0)
}

func TestTiktokenCounter_MultipleContentParts(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}
	text1 := "Part one"
	text2 := "Part two"
	text3 := "Part three"
	msg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text1},
			{Type: model.ContentTypeText, Text: &text2},
			{Type: model.ContentTypeText, Text: &text3},
		},
	}
	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, used, 0)
}

func TestTiktokenCounter_ContentPartsWithNonText(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}
	text := "Text content"
	msg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text},
			{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.com/image.png"}},
			{Type: model.ContentTypeText, Text: nil}, // nil text should be skipped
		},
	}
	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, used, 0)
}

func TestTiktokenCounter_AllContentTypes(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}
	text := "Additional text"
	msg := model.Message{
		Role:             model.RoleAssistant,
		Content:          "Main content",
		ReasoningContent: "Reasoning process",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text},
		},
	}
	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, used, 0)

	// Verify it's counting all parts
	mainTokens, _ := counter.CountTokens(context.Background(), model.Message{Content: "Main content"})
	reasoningTokens, _ := counter.CountTokens(context.Background(), model.Message{ReasoningContent: "Reasoning process"})
	partTokens, _ := counter.CountTokens(context.Background(), model.Message{ContentParts: []model.ContentPart{{Type: model.ContentTypeText, Text: &text}}})

	// Total should be approximately the sum (allowing for tokenization variations)
	expectedApprox := mainTokens + reasoningTokens + partTokens
	require.GreaterOrEqual(t, used, expectedApprox-2) // Allow small variance
}

func TestTiktokenCounter_LongMessage(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}
	longText := "This is a very long message that should result in a higher token count. " +
		"The more text we add, the more tokens we should get. " +
		"Token counting is an important feature for language models."
	msg := model.NewUserMessage(longText)
	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, used, 10) // Should have more than 10 tokens
}

func TestTiktokenCounter_DifferentModels(t *testing.T) {
	t.Run("gpt-4o", func(t *testing.T) {
		counter, err := New("gpt-4o")
		if err != nil {
			t.Skip("tiktoken-go not available: ", err)
		}
		msg := model.NewUserMessage("Hello")
		used, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		require.Greater(t, used, 0)
	})

	t.Run("gpt-4", func(t *testing.T) {
		counter, err := New("gpt-4")
		if err != nil {
			t.Skip("tiktoken-go not available: ", err)
		}
		msg := model.NewUserMessage("Hello")
		used, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		require.Greater(t, used, 0)
	})

	t.Run("gpt-3.5-turbo", func(t *testing.T) {
		counter, err := New("gpt-3.5-turbo")
		if err != nil {
			t.Skip("tiktoken-go not available: ", err)
		}
		msg := model.NewUserMessage("Hello")
		used, err := counter.CountTokens(context.Background(), msg)
		require.NoError(t, err)
		require.Greater(t, used, 0)
	})
}

func TestTiktokenCounter_WithToolCalls(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}

	// Test message with tool calls
	toolCall := model.ToolCall{
		Type: "function",
		ID:   "call_123",
		Function: model.FunctionDefinitionParam{
			Name:        "get_weather",
			Description: "Get the current weather",
			Arguments:   []byte(`{"location": "Beijing"}`),
		},
	}

	msg := model.Message{
		Role:      model.RoleAssistant,
		Content:   "I'll check the weather for you.",
		ToolCalls: []model.ToolCall{toolCall},
	}

	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, used, 0)

	// Verify tool calls contribute to token count
	contentOnlyMsg := model.Message{
		Role:    model.RoleAssistant,
		Content: "I'll check the weather for you.",
	}
	contentTokens, _ := counter.CountTokens(context.Background(), contentOnlyMsg)

	// Tool calls should add additional tokens
	require.Greater(t, used, contentTokens)
}

func TestTiktokenCounter_OnlyToolCalls(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}

	toolCall := model.ToolCall{
		Type: "function",
		ID:   "call_456",
		Function: model.FunctionDefinitionParam{
			Name:        "calculate",
			Description: "Perform mathematical calculations",
			Arguments:   []byte(`{"expression": "2+2"}`),
		},
	}

	msg := model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{toolCall},
	}

	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, used, 0)
}

func TestTiktokenCounter_MultipleToolCalls(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}

	toolCalls := []model.ToolCall{
		{
			Type: "function",
			ID:   "call_weather",
			Function: model.FunctionDefinitionParam{
				Name:        "get_weather",
				Description: "Get weather information",
				Arguments:   []byte(`{"location": "Shanghai"}`),
			},
		},
		{
			Type: "function",
			ID:   "call_time",
			Function: model.FunctionDefinitionParam{
				Name:        "get_time",
				Description: "Get current time",
				Arguments:   []byte(`{"timezone": "UTC"}`),
			},
		},
	}

	msg := model.Message{
		Role:      model.RoleAssistant,
		Content:   "Here are multiple tool calls:",
		ToolCalls: toolCalls,
	}

	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, used, 0)

	// Compare with single tool call
	singleToolMsg := model.Message{
		Role:      model.RoleAssistant,
		Content:   "Here are multiple tool calls:",
		ToolCalls: []model.ToolCall{toolCalls[0]},
	}
	singleTokens, _ := counter.CountTokens(context.Background(), singleToolMsg)

	// Multiple tool calls should have more tokens
	require.Greater(t, used, singleTokens)
}

func TestTiktokenCounter_EmptyToolCall(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}

	// Test empty tool call
	emptyToolCall := model.ToolCall{}
	msg := model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{emptyToolCall},
	}

	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.GreaterOrEqual(t, used, 0)
}

func TestTiktokenCounter_ToolCallArgumentsOnly(t *testing.T) {
	counter, err := New("gpt-4o")
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}

	// Test tool call with only arguments
	toolCall := model.ToolCall{
		Function: model.FunctionDefinitionParam{
			Arguments: []byte(`{"key": "value", "number": 123, "array": [1, 2, 3]}`),
		},
	}

	msg := model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{toolCall},
	}

	used, err := counter.CountTokens(context.Background(), msg)
	require.NoError(t, err)
	require.Greater(t, used, 0)
}

func newWithCodec(codec tokenizer.Codec) *Counter {
	return &Counter{encoding: codec}
}
func TestTiktokenCounter_ContentEncodingError(t *testing.T) {
	counter := newWithCodec(&mockCodec{shouldFail: true})

	msg := model.Message{
		Role:    model.RoleUser,
		Content: "test content",
	}

	_, err := counter.CountTokens(context.Background(), msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encode content failed")
}

func TestTiktokenCounter_ReasoningContentEncodingError(t *testing.T) {
	counter := newWithCodec(&mockCodec{shouldFail: true})

	msg := model.Message{
		Role:             model.RoleAssistant,
		ReasoningContent: "test reasoning",
	}

	_, err := counter.CountTokens(context.Background(), msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encode reasoning failed")
}

func TestTiktokenCounter_ContentPartsEncodingError(t *testing.T) {
	counter := newWithCodec(&mockCodec{shouldFail: true})

	text := "test content part"
	msg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text},
		},
	}

	_, err := counter.CountTokens(context.Background(), msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encode text part failed")
}

func TestTiktokenCounter_ToolCallTypeEncodingError(t *testing.T) {
	counter := newWithCodec(&mockCodec{shouldFail: true})

	toolCall := model.ToolCall{
		Type: "function",
	}

	msg := model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{toolCall},
	}

	_, err := counter.CountTokens(context.Background(), msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encode tool call type failed")
}

func TestTiktokenCounter_ToolCallIDEncodingError(t *testing.T) {
	counter := newWithCodec(&mockCodec{shouldFail: true})

	toolCall := model.ToolCall{
		ID: "call_123",
	}

	msg := model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{toolCall},
	}

	_, err := counter.CountTokens(context.Background(), msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encode tool call ID failed")
}

func TestTiktokenCounter_FunctionNameEncodingError(t *testing.T) {
	counter := newWithCodec(&mockCodec{shouldFail: true})

	toolCall := model.ToolCall{
		Function: model.FunctionDefinitionParam{
			Name: "test_function",
		},
	}

	msg := model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{toolCall},
	}

	_, err := counter.CountTokens(context.Background(), msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encode function name failed")
}

func TestTiktokenCounter_FunctionDescriptionEncodingError(t *testing.T) {
	counter := newWithCodec(&mockCodec{shouldFail: true})

	toolCall := model.ToolCall{
		Function: model.FunctionDefinitionParam{
			Description: "test description",
		},
	}

	msg := model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{toolCall},
	}

	_, err := counter.CountTokens(context.Background(), msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encode function description failed")
}

func TestTiktokenCounter_FunctionArgumentsEncodingError(t *testing.T) {
	counter := newWithCodec(&mockCodec{shouldFail: true})

	toolCall := model.ToolCall{
		Function: model.FunctionDefinitionParam{
			Arguments: []byte(`{"key": "value"}`),
		},
	}

	msg := model.Message{
		Role:      model.RoleAssistant,
		ToolCalls: []model.ToolCall{toolCall},
	}

	_, err := counter.CountTokens(context.Background(), msg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encode function arguments failed")
}
