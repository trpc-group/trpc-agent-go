//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestRunInputFromMessages_UserMessage(t *testing.T) {
	messages := []model.Message{
		{Role: model.RoleSystem, Content: "sys"},
		{Role: model.RoleUser, Content: "hello"},
	}

	got, err := runInputFromMessages(messages)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, model.RoleUser, got.inputMessage.Role)
	assert.Equal(t, "hello", got.inputMessage.Content)
	require.Len(t, got.history, 1)
	assert.Equal(t, model.RoleSystem, got.history[0].Role)
	assert.Empty(t, got.toolMessages)
}

func TestRunInputFromMessages_RejectsEmptyMessages(t *testing.T) {
	got, err := runInputFromMessages(nil)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorContains(t, err, "messages cannot be empty")
}

func TestRunInputFromMessages_AllowsAssistantLast(t *testing.T) {
	messages := []model.Message{
		{Role: model.RoleUser, Content: "hi"},
		{Role: model.RoleAssistant, Content: "reply"},
	}

	got, err := runInputFromMessages(messages)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, model.RoleAssistant, got.inputMessage.Role)
	assert.Equal(t, "reply", got.inputMessage.Content)
	require.Len(t, got.history, 1)
	assert.Equal(t, model.RoleUser, got.history[0].Role)
}

func TestRunInputFromMessages_SingleToolResult(t *testing.T) {
	messages := []model.Message{
		{Role: model.RoleUser, Content: "search"},
		{
			Role:      model.RoleAssistant,
			ToolCalls: []model.ToolCall{{ID: "call-1", Function: model.FunctionDefinitionParam{Name: "search"}}},
		},
		{
			Role:     model.RoleTool,
			ToolID:   "call-1",
			ToolName: "search",
			Content:  "result",
		},
	}

	got, err := runInputFromMessages(messages)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, model.RoleTool, got.inputMessage.Role)
	assert.Equal(t, "call-1", got.inputMessage.ToolID)
	assert.Equal(t, "result", got.inputMessage.Content)
	require.Len(t, got.history, 2)
	require.Len(t, got.toolMessages, 1)
}

func TestRunInputFromMessages_CollectsTailToolMessages(t *testing.T) {
	messages := []model.Message{
		{Role: model.RoleTool, ToolID: "old-call", Content: "old"},
		{Role: model.RoleAssistant, Content: "calling tools"},
		{Role: model.RoleTool, ToolID: "call-1", ToolName: "search", Content: "result 1"},
		{Role: model.RoleTool, ToolID: "call-2", ToolName: "lookup", Content: "result 2"},
	}

	got, err := runInputFromMessages(messages)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "call-2", got.inputMessage.ToolID)
	assert.Equal(t, "result 2", got.inputMessage.Content)
	require.Len(t, got.history, 2)
	assert.Equal(t, "old-call", got.history[0].ToolID)
	assert.Equal(t, model.RoleAssistant, got.history[1].Role)
	require.Len(t, got.toolMessages, 2)
	assert.Equal(t, "call-1", got.toolMessages[0].ToolID)
	assert.Equal(t, "call-2", got.toolMessages[1].ToolID)
}

func TestRunInputFromMessages_RejectsToolMessageMissingID(t *testing.T) {
	messages := []model.Message{
		{Role: model.RoleAssistant, Content: "calling tools"},
		{Role: model.RoleTool, Content: "result"},
	}

	got, err := runInputFromMessages(messages)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorContains(t, err, errToolMessageMissingID)
}

func TestRunInputFromMessages_RejectsMultimodalToolResult(t *testing.T) {
	text := "result"
	messages := []model.Message{
		{Role: model.RoleAssistant, Content: "calling tools"},
		{
			Role:   model.RoleTool,
			ToolID: "call-1",
			ContentParts: []model.ContentPart{
				{Type: model.ContentTypeText, Text: &text},
			},
		},
	}

	got, err := runInputFromMessages(messages)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorContains(t, err, errToolMessageNotString)
}

func TestRunInputFromMessages_RejectsMixedTextAndContentPartsToolResult(t *testing.T) {
	text := "image caption"
	messages := []model.Message{
		{Role: model.RoleAssistant, Content: "calling tools"},
		{
			Role:    model.RoleTool,
			ToolID:  "call-1",
			Content: "result text",
			ContentParts: []model.ContentPart{
				{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.com/a.png"}},
				{Type: model.ContentTypeText, Text: &text},
			},
		},
	}

	got, err := runInputFromMessages(messages)

	require.Error(t, err)
	assert.Nil(t, got)
	assert.ErrorContains(t, err, errToolMessageNotString)
}

func TestWithToolResultMessageRewriter_MergesParallelResults(t *testing.T) {
	toolMessages := []model.Message{
		{Role: model.RoleTool, ToolID: "call-1", ToolName: "search", Content: "result 1"},
		{Role: model.RoleTool, ToolID: "call-2", ToolName: "lookup", Content: "result 2"},
	}
	opts := agent.NewRunOptions(withToolResultMessageRewriter(toolMessages))

	require.NotNil(t, opts.UserMessageRewriter)
	currentTurn, err := opts.UserMessageRewriter(
		context.Background(),
		&agent.UserMessageRewriteArgs{
			OriginalMessage: toolMessages[1],
		},
	)
	require.NoError(t, err)
	require.Len(t, currentTurn, 2)
	assert.Equal(t, "call-1", currentTurn[0].ToolID)
	assert.Equal(t, "result 1", currentTurn[0].Content)
	assert.Equal(t, "call-2", currentTurn[1].ToolID)
	assert.Equal(t, "result 2", currentTurn[1].Content)
}

func TestWithToolResultMessageRewriter_SkipsSingleToolResult(t *testing.T) {
	toolMessages := []model.Message{
		{Role: model.RoleTool, ToolID: "call-1", Content: "result"},
	}
	opts := agent.NewRunOptions(withToolResultMessageRewriter(toolMessages))

	assert.Nil(t, opts.UserMessageRewriter)
}

func TestWithToolResultMessageRewriter_WrapsExistingRewriter(t *testing.T) {
	toolMessages := []model.Message{
		{Role: model.RoleTool, ToolID: "call-1", ToolName: "search", Content: "result 1"},
		{Role: model.RoleTool, ToolID: "call-2", ToolName: "lookup", Content: "result 2"},
	}
	var customRewriterCalled bool
	opts := agent.NewRunOptions(
		agent.WithUserMessageRewriter(func(
			context.Context,
			*agent.UserMessageRewriteArgs,
		) ([]model.Message, error) {
			customRewriterCalled = true
			return []model.Message{
				model.NewUserMessage("custom"),
				model.NewToolMessage("call-2", "lookup", "rewritten duplicate"),
			}, nil
		}),
		withToolResultMessageRewriter(toolMessages),
	)

	require.NotNil(t, opts.UserMessageRewriter)
	currentTurn, err := opts.UserMessageRewriter(
		context.Background(),
		&agent.UserMessageRewriteArgs{
			OriginalMessage: toolMessages[1],
		},
	)
	require.NoError(t, err)
	require.Len(t, currentTurn, 3)
	assert.Equal(t, model.RoleUser, currentTurn[0].Role)
	assert.Equal(t, "custom", currentTurn[0].Content)
	assert.Equal(t, "call-1", currentTurn[1].ToolID)
	assert.Equal(t, "result 1", currentTurn[1].Content)
	assert.Equal(t, "call-2", currentTurn[2].ToolID)
	assert.Equal(t, "rewritten duplicate", currentTurn[2].Content)
	assert.True(t, customRewriterCalled)
}

func TestWithToolResultMessageRewriter_PropagatesRewriterError(t *testing.T) {
	toolMessages := []model.Message{
		{Role: model.RoleTool, ToolID: "call-1", Content: "result 1"},
		{Role: model.RoleTool, ToolID: "call-2", Content: "result 2"},
	}
	opts := agent.NewRunOptions(
		agent.WithUserMessageRewriter(func(
			context.Context,
			*agent.UserMessageRewriteArgs,
		) ([]model.Message, error) {
			return nil, errors.New("rewrite failed")
		}),
		withToolResultMessageRewriter(toolMessages),
	)

	_, err := opts.UserMessageRewriter(
		context.Background(),
		&agent.UserMessageRewriteArgs{OriginalMessage: toolMessages[1]},
	)
	require.Error(t, err)
	assert.ErrorContains(t, err, "rewrite failed")
}

func TestMergeToolResultRewriteMessages_KeepsNonToolMessagesWithToolID(t *testing.T) {
	rewritten := []model.Message{
		{Role: model.RoleUser, Content: "context", ToolID: "call-1"},
		model.NewToolMessage("call-1", "search", "rewritten duplicate"),
	}
	toolResults := []model.Message{
		model.NewToolMessage("call-1", "search", "authoritative result"),
	}

	got := mergeToolResultRewriteMessages(rewritten, toolResults)

	require.Len(t, got, 2)
	assert.Equal(t, model.RoleUser, got[0].Role)
	assert.Equal(t, "context", got[0].Content)
	assert.Equal(t, "call-1", got[0].ToolID)
	assert.Equal(t, model.RoleTool, got[1].Role)
	assert.Equal(t, "rewritten duplicate", got[1].Content)
	assert.Equal(t, "call-1", got[1].ToolID)
}

func TestMergeToolResultRewriteMessages_AppendsUnmatchedToolResults(t *testing.T) {
	rewritten := []model.Message{
		model.NewUserMessage("context"),
	}
	toolResults := []model.Message{
		model.NewToolMessage("call-1", "search", "result 1"),
		model.NewToolMessage("call-2", "lookup", "result 2"),
	}

	got := mergeToolResultRewriteMessages(rewritten, toolResults)

	require.Len(t, got, 3)
	assert.Equal(t, "context", got[0].Content)
	assert.Equal(t, "call-1", got[1].ToolID)
	assert.Equal(t, "result 1", got[1].Content)
	assert.Equal(t, "call-2", got[2].ToolID)
	assert.Equal(t, "result 2", got[2].Content)
}

func TestBuildRunOptions_IncludesToolResultRewriter(t *testing.T) {
	input := &runInputMessages{
		inputMessage: model.Message{Role: model.RoleTool, ToolID: "call-2", Content: "result 2"},
		history: []model.Message{
			{Role: model.RoleAssistant, Content: "calling tools"},
		},
		toolMessages: []model.Message{
			{Role: model.RoleTool, ToolID: "call-1", Content: "result 1"},
			{Role: model.RoleTool, ToolID: "call-2", Content: "result 2"},
		},
	}

	runOpts, err := buildRunOptions(&openAIRequest{}, input)

	require.NoError(t, err)
	opts := agent.NewRunOptions(runOpts...)
	require.NotNil(t, opts.UserMessageRewriter)
	require.Len(t, opts.Messages, 1)
	assert.Equal(t, model.RoleAssistant, opts.Messages[0].Role)
}

func TestBuildRunOptions_IncludesExternalTools(t *testing.T) {
	input := &runInputMessages{
		inputMessage: model.Message{Role: model.RoleUser, Content: "search"},
	}
	req := &openAIRequest{
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name: "client_search",
				},
			},
		},
	}

	runOpts, err := buildRunOptions(req, input)

	require.NoError(t, err)
	opts := agent.NewRunOptions(runOpts...)
	require.Len(t, opts.ExternalTools, 1)
	assert.Equal(t, "client_search", opts.ExternalTools[0].Declaration().Name)
}

func TestBuildRunOptions_SkipsExternalToolsWhenToolChoiceNone(t *testing.T) {
	input := &runInputMessages{
		inputMessage: model.Message{Role: model.RoleUser, Content: "search"},
	}
	req := &openAIRequest{
		ToolChoice: openAIToolChoiceNone,
		Tools: []openAITool{
			{
				Type:     "function",
				Function: openAIFunction{Name: "client_search"},
			},
		},
	}

	runOpts, err := buildRunOptions(req, input)

	require.NoError(t, err)
	opts := agent.NewRunOptions(runOpts...)
	assert.Empty(t, opts.ExternalTools)
}
