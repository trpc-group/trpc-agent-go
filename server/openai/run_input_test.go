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
