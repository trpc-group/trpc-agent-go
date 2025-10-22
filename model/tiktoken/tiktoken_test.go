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
