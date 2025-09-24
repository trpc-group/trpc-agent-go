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
	counter, err := New("gpt-4o", 4000)
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}
	msgs := []model.Message{
		model.NewUserMessage("Hello, world!"),
		model.NewAssistantMessage("Hi there!"),
	}
	used, err := counter.CountTokens(context.Background(), msgs)
	require.NoError(t, err)
	require.Greater(t, used, 0)
	rem, err := counter.RemainingTokens(context.Background(), msgs)
	require.NoError(t, err)
	require.Equal(t, 4000-used, rem)
}

func TestTiktokenCounter_ModelFallback(t *testing.T) {
	counter, err := New("unknown-model-name-xyz", 3000)
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}
	msgs := []model.Message{
		model.NewUserMessage("alpha beta gamma"),
	}
	used, err := counter.CountTokens(context.Background(), msgs)
	require.NoError(t, err)
	require.Greater(t, used, 0)
	rem, err := counter.RemainingTokens(context.Background(), msgs)
	require.NoError(t, err)
	require.Equal(t, 3000-used, rem)
}

func TestTiktokenCounter_ContentPartsAndReasoning(t *testing.T) {
	counter, err := New("gpt-4", 5000)
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
	used, err := counter.CountTokens(context.Background(), []model.Message{msg})
	require.NoError(t, err)
	require.Greater(t, used, 0)
}

func TestTiktokenCounter_EmptyMessages(t *testing.T) {
	counter, err := New("gpt-4o", 100)
	if err != nil {
		t.Skip("tiktoken-go not available: ", err)
	}
	used, err := counter.CountTokens(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, 0, used)
	rem, err := counter.RemainingTokens(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, 100, rem)
}
