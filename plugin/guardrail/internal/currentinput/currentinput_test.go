//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package currentinput

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	guardtranscript "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/internal/transcript"
)

type fixedTokenCounter struct {
	count int
	err   error
}

func (c fixedTokenCounter) CountTokens(ctx context.Context, message model.Message) (int, error) {
	return c.count, c.err
}

func (c fixedTokenCounter) CountTokensRange(
	ctx context.Context,
	messages []model.Message,
	start, end int,
) (int, error) {
	return c.count, c.err
}

func TestBuild_KeepsLatestUserInputOutsideTranscript(t *testing.T) {
	req := Build(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "Earlier user context."},
		{Role: model.RoleAssistant, Content: "Assistant context."},
		{Role: model.RoleUser, Content: "Latest user input."},
	}, fixedTokenCounter{count: 1}, func(entry guardtranscript.Entry) guardtranscript.Entry {
		return entry
	})
	require.NotNil(t, req)
	require.Len(t, req.Transcript, 2)
	assert.Equal(t, model.RoleUser, req.Transcript[0].Role)
	assert.Equal(t, "Earlier user context.", req.Transcript[0].Content)
	assert.Equal(t, model.RoleAssistant, req.Transcript[1].Role)
	assert.Equal(t, "Assistant context.", req.Transcript[1].Content)
	assert.Equal(t, "Latest user input.", req.LastUserInput)
}

func TestBuild_KeepsFullLatestUserInput(t *testing.T) {
	longInput := repeat("user ", guardtranscript.DefaultMessageEntryCap+10)
	req := Build(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: longInput},
	}, fixedTokenCounter{count: 1}, func(entry guardtranscript.Entry) guardtranscript.Entry {
		return entry
	})
	require.NotNil(t, req)
	require.Empty(t, req.Transcript)
	assert.Equal(t, longInput, req.LastUserInput)
}

func TestBuild_CountTokenFailureFallsBackToOmission(t *testing.T) {
	req := Build(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "Latest user input."},
		{Role: model.RoleAssistant, Content: "Assistant context."},
	}, fixedTokenCounter{err: errors.New("count tokens failed")}, func(entry guardtranscript.Entry) guardtranscript.Entry {
		return entry
	})
	require.NotNil(t, req)
	require.Len(t, req.Transcript, 1)
	assert.Equal(t, model.RoleAssistant, req.Transcript[0].Role)
	assert.Equal(t, guardtranscript.DefaultOmissionNote, req.Transcript[0].Content)
	assert.Equal(t, "Latest user input.", req.LastUserInput)
}

func TestBuild_WithoutLatestUserInputReturnsNil(t *testing.T) {
	req := Build(context.Background(), []model.Message{
		{Role: model.RoleAssistant, Content: "Assistant context."},
		{Role: model.RoleTool, Content: "Tool context."},
	}, fixedTokenCounter{count: 1}, func(entry guardtranscript.Entry) guardtranscript.Entry {
		return entry
	})
	require.Nil(t, req)
}

func TestBuild_WithoutTextInLatestUserMessageReturnsNil(t *testing.T) {
	req := Build(context.Background(), []model.Message{
		{Role: model.RoleUser, Content: "Earlier user context."},
		{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{{
				Type:  model.ContentTypeImage,
				Image: &model.Image{URL: "https://example.com/image.png"},
			}},
		},
	}, fixedTokenCounter{count: 1}, func(entry guardtranscript.Entry) guardtranscript.Entry {
		return entry
	})
	require.Nil(t, req)
}

func repeat(value string, n int) string {
	if n <= 0 {
		return ""
	}
	result := make([]byte, 0, len(value)*n)
	for i := 0; i < n; i++ {
		result = append(result, value...)
	}
	return string(result)
}
