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
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

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
